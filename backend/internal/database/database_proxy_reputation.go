package database

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"magpie/internal/domain"
	"magpie/internal/support/reputation"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	reputationSampleLimit     = 20
	reputationDefaultProtocol = "unknown"
	// PostgreSQL allows at most 65535 bind parameters per query, so keep slices below that.
	reputationBatchSize = 60000
	// Each upsert touches roughly 7 columns plus conflict updates, so keep batches small.
	reputationUpsertBatchSize = 4000
)

type proxyReputationInput struct {
	ProxyID       uint64
	EstimatedType string
	FailureStreak uint16
	Samples       map[string][]reputationSample
	AbuseIPDB     *domain.AbuseIPDBCheck
}

type reputationSample struct {
	ProxyID    uint64
	Protocol   string
	Alive      bool
	ResponseMS uint16
	CreatedAt  time.Time
	Level      string
}

type proxyReputationSummary struct {
	Reputation domain.ProxyReputation
	Result     reputation.ScoreResult
	Metrics    reputation.Metrics
}

// RecalculateProxyReputations recomputes per-proxy, per-protocol reputation scores for the given proxy IDs.
func RecalculateProxyReputations(ctx context.Context, proxyIDs []uint64) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	if DB == nil {
		return fmt.Errorf("database not initialised")
	}

	for start := 0; start < len(proxyIDs); start += reputationBatchSize {
		end := start + reputationBatchSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}

		if err := recalculateProxyReputationsBatch(ctx, proxyIDs[start:end]); err != nil {
			return err
		}
	}

	return nil
}

func recalculateProxyReputationsBatch(ctx context.Context, proxyIDs []uint64) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	if DB == nil {
		return fmt.Errorf("database not initialised")
	}

	inputs, err := loadProxyReputationInputs(ctx, proxyIDs)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	reputations := make([]domain.ProxyReputation, 0, len(proxyIDs)*3)

	for _, input := range inputs {
		if len(input.Samples) == 0 && input.AbuseIPDB == nil {
			continue
		}

		perProtocolResults := make(map[string]proxyReputationSummary, len(input.Samples))
		for proto, samples := range input.Samples {
			metrics := buildMetrics(samples, input)
			result := reputation.Score(metrics, now, nil)

			signals, err := json.Marshal(result.Signals)
			if err != nil {
				log.Error("reputation: marshal signals", "proxy_id", input.ProxyID, "protocol", proto, "error", err)
				continue
			}

			perProtocolResults[proto] = proxyReputationSummary{
				Reputation: domain.ProxyReputation{
					ProxyID:      input.ProxyID,
					Kind:         proto,
					Score:        float32(result.Score),
					Label:        result.Label,
					Signals:      signals,
					CalculatedAt: now,
				},
				Result:  result,
				Metrics: metrics,
			}
		}

		if len(perProtocolResults) == 0 && input.AbuseIPDB == nil {
			continue
		}

		for _, summary := range perProtocolResults {
			reputations = append(reputations, summary.Reputation)
		}

		overallReputation, err := buildOverallReputation(input, perProtocolResults, now)
		if err != nil {
			log.Error("reputation: overall score", "proxy_id", input.ProxyID, "error", err)
			continue
		}
		if overallReputation != nil {
			reputations = append(reputations, *overallReputation)
		}
	}

	if len(reputations) == 0 {
		return nil
	}

	return upsertProxyReputations(ctx, reputations)
}

func loadProxyReputationInputs(ctx context.Context, proxyIDs []uint64) (map[uint64]*proxyReputationInput, error) {
	db := DB.WithContext(ctx)

	var proxyRows []struct {
		ID            uint64
		EstimatedType string
	}

	if err := db.
		Model(&domain.Proxy{}).
		Select("id", "estimated_type").
		Where("id IN ?", proxyIDs).
		Scan(&proxyRows).Error; err != nil {
		return nil, fmt.Errorf("load proxies for reputation: %w", err)
	}

	inputs := make(map[uint64]*proxyReputationInput, len(proxyRows))
	for _, row := range proxyRows {
		inputs[row.ID] = &proxyReputationInput{
			ProxyID:       row.ID,
			EstimatedType: row.EstimatedType,
			Samples:       make(map[string][]reputationSample),
		}
	}

	if len(inputs) == 0 {
		return inputs, nil
	}

	var failureRows []struct {
		ProxyID     uint64
		MaxFailures uint16
	}

	if err := db.
		Table("user_proxies").
		Select("proxy_id", "MAX(consecutive_failures) AS max_failures").
		Where("proxy_id IN ?", proxyIDs).
		Group("proxy_id").
		Scan(&failureRows).Error; err != nil {
		return nil, fmt.Errorf("load proxy failure streaks: %w", err)
	}

	for _, row := range failureRows {
		if input, ok := inputs[row.ProxyID]; ok {
			input.FailureStreak = row.MaxFailures
		}
	}

	statRows, err := loadReputationSamples(ctx, proxyIDs)
	if err != nil {
		return nil, err
	}

	for _, sample := range statRows {
		input, ok := inputs[sample.ProxyID]
		if !ok {
			continue
		}
		proto := sample.Protocol
		if proto == "" {
			proto = reputationDefaultProtocol
		}
		input.Samples[proto] = append(input.Samples[proto], sample)
	}

	var abuseRows []domain.AbuseIPDBCheck
	if err := db.
		Where("proxy_id IN ?", proxyIDs).
		Find(&abuseRows).Error; err != nil {
		return nil, fmt.Errorf("load abuseipdb checks for reputation: %w", err)
	}
	for i := range abuseRows {
		row := abuseRows[i]
		if input, ok := inputs[row.ProxyID]; ok {
			input.AbuseIPDB = &row
		}
	}

	return inputs, nil
}

func loadReputationSamples(ctx context.Context, proxyIDs []uint64) ([]reputationSample, error) {
	if len(proxyIDs) == 0 {
		return nil, nil
	}

	query := `
WITH ranked AS (
	SELECT
		ps.proxy_id,
		LOWER(protocols.name) AS protocol,
		ps.alive,
		ps.response_time,
		ps.created_at,
		COALESCE(LOWER(al.name), '') AS level,
		ROW_NUMBER() OVER (PARTITION BY ps.proxy_id, ps.protocol_id ORDER BY ps.created_at DESC) AS rn
	FROM proxy_statistics ps
	JOIN protocols ON protocols.id = ps.protocol_id
	LEFT JOIN anonymity_levels al ON al.id = ps.level_id
	WHERE ps.proxy_id IN ?
)
SELECT
	proxy_id,
	protocol,
	alive,
	response_time,
	created_at,
	level
FROM ranked
WHERE rn <= ?
ORDER BY proxy_id, protocol, rn;
`

	var rows []reputationSample

	if err := DB.WithContext(ctx).
		Raw(query, proxyIDs, reputationSampleLimit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load proxy statistics for reputation: %w", err)
	}

	return rows, nil
}

func buildMetrics(samples []reputationSample, input *proxyReputationInput) reputation.Metrics {
	total := len(samples)
	success := 0
	responseTimes := make([]uint16, 0, total)

	var latestCheck *time.Time
	var latestSuccess *time.Time

	bestAnonymity := ""

	var earliest time.Time
	if total > 0 {
		latestCheck = new(samples[0].CreatedAt)
		earliest = samples[len(samples)-1].CreatedAt
		for _, sample := range samples {
			if sample.Alive {
				success++
				if latestSuccess == nil {
					latestSuccess = new(sample.CreatedAt)
				}
			}
			if sample.ResponseMS > 0 {
				responseTimes = append(responseTimes, sample.ResponseMS)
			}
			bestAnonymity = pickBetterAnonymity(bestAnonymity, sample.Level)
		}
	}

	windowHours := 0.0
	if !earliest.IsZero() && latestCheck != nil {
		windowHours = latestCheck.Sub(earliest).Hours()
		if windowHours < 0 {
			windowHours = 0
		}
	}

	return reputation.Metrics{
		TotalChecks:       total,
		SuccessfulChecks:  success,
		ResponseTimesMS:   responseTimes,
		LatestCheck:       latestCheck,
		LatestSuccess:     latestSuccess,
		BestAnonymity:     bestAnonymity,
		EstimatedType:     input.EstimatedType,
		FailureStreak:     input.FailureStreak,
		SampleWindowHours: windowHours,
	}
}

func pickBetterAnonymity(current, candidate string) string {
	order := map[string]int{
		"elite":       3,
		"anonymous":   2,
		"transparent": 1,
		"":            0,
	}

	currentRank := order[strings.ToLower(current)]
	candidateRank := order[strings.ToLower(candidate)]

	if candidateRank > currentRank {
		return candidate
	}

	return current
}

func buildOverallReputation(input *proxyReputationInput, summaries map[string]proxyReputationSummary, now time.Time) (*domain.ProxyReputation, error) {
	allSamples := make([]reputationSample, 0)
	for _, samples := range input.Samples {
		allSamples = append(allSamples, samples...)
	}
	if len(allSamples) == 0 && input.AbuseIPDB == nil {
		return nil, nil
	}

	metrics := buildMetrics(allSamples, input)
	result := reputation.Score(metrics, now, nil)
	finalScore := result.Score
	abuseSignals := map[string]any(nil)
	if input.AbuseIPDB != nil {
		abuseScore := clampReputationScore(100 - float64(input.AbuseIPDB.AbuseConfidenceScore))
		if len(allSamples) == 0 {
			finalScore = abuseScore
		} else {
			finalScore = clampReputationScore(result.Score*0.2 + abuseScore*0.8)
		}
		abuseSignals = map[string]any{
			"abuse_confidence_score": input.AbuseIPDB.AbuseConfidenceScore,
			"reputation_score":       abuseScore,
			"checked_at":             input.AbuseIPDB.CheckedAt,
			"total_reports":          input.AbuseIPDB.TotalReports,
			"num_distinct_users":     input.AbuseIPDB.NumDistinctUsers,
			"usage_type":             input.AbuseIPDB.UsageType,
			"isp":                    input.AbuseIPDB.ISP,
			"domain":                 input.AbuseIPDB.Domain,
			"country_code":           input.AbuseIPDB.CountryCode,
			"is_whitelisted":         input.AbuseIPDB.IsWhitelisted,
			"is_tor":                 input.AbuseIPDB.IsTor,
			"weight":                 0.8,
		}
		if input.AbuseIPDB.LastReportedAt != nil {
			abuseSignals["last_reported_at"] = *input.AbuseIPDB.LastReportedAt
		}
	}

	components := make(map[string]any, len(summaries))
	for proto, summary := range summaries {
		components[proto] = map[string]any{
			"score":   summary.Result.Score,
			"label":   summary.Result.Label,
			"checks":  summary.Metrics.TotalChecks,
			"success": summary.Metrics.SuccessfulChecks,
		}
	}

	signals := map[string]any{
		"components": components,
		"combined":   result.Signals,
	}
	if abuseSignals != nil {
		signals["abuseipdb"] = abuseSignals
	}

	payload, err := json.Marshal(signals)
	if err != nil {
		return nil, fmt.Errorf("marshal overall signals: %w", err)
	}

	return &domain.ProxyReputation{
		ProxyID:      input.ProxyID,
		Kind:         domain.ProxyReputationKindOverall,
		Score:        float32(finalScore),
		Label:        reputationLabelFromScore(finalScore),
		Signals:      payload,
		CalculatedAt: now,
	}, nil
}

func clampReputationScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func reputationLabelFromScore(score float64) string {
	switch {
	case score >= 80:
		return "good"
	case score >= 40:
		return "neutral"
	default:
		return "poor"
	}
}

func upsertProxyReputations(ctx context.Context, reputations []domain.ProxyReputation) error {
	if len(reputations) == 0 {
		return nil
	}

	db := DB.WithContext(ctx)
	useConstraintlessFallback := false

	for start := 0; start < len(reputations); start += reputationUpsertBatchSize {
		end := start + reputationUpsertBatchSize
		if end > len(reputations) {
			end = len(reputations)
		}
		chunk := reputations[start:end]

		if useConstraintlessFallback {
			if err := upsertProxyReputationsChunkWithoutConstraint(db, chunk); err != nil {
				return err
			}
			continue
		}

		if err := upsertProxyReputationsChunk(db, chunk); err != nil {
			if isMissingUniqueConstraintError(err) {
				// Avoid full-table dedupe in the hot path. Try creating the index without cleanup.
				if ensureErr := ensureProxyReputationIndex(db); ensureErr != nil {
					log.Warn("reputation: ensure unique index failed; falling back to constraintless upsert", "error", ensureErr)
				} else if retryErr := upsertProxyReputationsChunk(db, chunk); retryErr == nil {
					continue
				} else if !isMissingUniqueConstraintError(retryErr) {
					return retryErr
				}

				useConstraintlessFallback = true
				log.Warn("reputation: missing unique constraint; using fallback upsert strategy")
				if fallbackErr := upsertProxyReputationsChunkWithoutConstraint(db, chunk); fallbackErr != nil {
					return fallbackErr
				}
				continue
			}
			return err
		}
	}

	proxyIDs := make([]uint64, 0, len(reputations))
	seen := make(map[uint64]struct{}, len(reputations))
	for _, rep := range reputations {
		if rep.ProxyID == 0 {
			continue
		}
		if _, exists := seen[rep.ProxyID]; exists {
			continue
		}
		seen[rep.ProxyID] = struct{}{}
		proxyIDs = append(proxyIDs, rep.ProxyID)
	}
	QueueReadModelRefreshForProxyIDs(proxyIDs)
	return nil
}

func upsertProxyReputationsChunk(db *gorm.DB, reps []domain.ProxyReputation) error {
	if len(reps) == 0 {
		return nil
	}

	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "proxy_id"}, {Name: "kind"}},
		DoUpdates: clause.AssignmentColumns([]string{"score", "label", "signals", "calculated_at", "updated_at"}),
	}).Create(&reps).Error
}

func upsertProxyReputationsChunkWithoutConstraint(db *gorm.DB, reps []domain.ProxyReputation) error {
	if len(reps) == 0 {
		return nil
	}
	if db == nil {
		return fmt.Errorf("database not initialised")
	}

	deduped := dedupeProxyReputationsForUpsert(reps)
	now := time.Now().UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		for _, rep := range deduped {
			lockKey := proxyReputationAdvisoryLockKey(rep.ProxyID, rep.Kind)
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockKey).Error; err != nil {
				return err
			}

			result := tx.Model(&domain.ProxyReputation{}).
				Where("proxy_id = ? AND kind = ?", rep.ProxyID, rep.Kind).
				Updates(map[string]any{
					"score":         rep.Score,
					"label":         rep.Label,
					"signals":       rep.Signals,
					"calculated_at": rep.CalculatedAt,
					"updated_at":    now,
				})
			if result.Error != nil {
				return result.Error
			}

			if result.RowsAffected > 0 {
				continue
			}

			if err := tx.Create(&rep).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func proxyReputationAdvisoryLockKey(proxyID uint64, kind string) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(fmt.Sprintf("%d:%s", proxyID, strings.ToLower(strings.TrimSpace(kind)))))
	return int64(hasher.Sum64())
}

func dedupeProxyReputationsForUpsert(reps []domain.ProxyReputation) []domain.ProxyReputation {
	if len(reps) <= 1 {
		return reps
	}

	type key struct {
		proxyID uint64
		kind    string
	}

	out := make([]domain.ProxyReputation, 0, len(reps))
	indexByKey := make(map[key]int, len(reps))

	for _, rep := range reps {
		k := key{
			proxyID: rep.ProxyID,
			kind:    strings.ToLower(strings.TrimSpace(rep.Kind)),
		}

		if idx, exists := indexByKey[k]; exists {
			out[idx] = rep
			continue
		}

		indexByKey[k] = len(out)
		out = append(out, rep)
	}

	return out
}

func isMissingUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "42P10")
}

func GetProxyReputations(ctx context.Context, proxyIDs []uint64) (map[uint64][]domain.ProxyReputation, error) {
	result := make(map[uint64][]domain.ProxyReputation, len(proxyIDs))
	if len(proxyIDs) == 0 {
		return result, nil
	}
	if DB == nil {
		return nil, fmt.Errorf("database not initialised")
	}

	db := DB.WithContext(ctx)

	var rows []domain.ProxyReputation
	if err := db.
		Where("proxy_id IN ?", proxyIDs).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load proxy reputations: %w", err)
	}

	for _, row := range rows {
		result[row.ProxyID] = append(result[row.ProxyID], row)
	}

	return result, nil
}

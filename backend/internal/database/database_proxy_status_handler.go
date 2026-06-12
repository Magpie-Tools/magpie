package database

import (
	"fmt"
	"time"

	"magpie/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type proxyProtocolKey struct {
	ProxyID    uint64
	ProtocolID int
}

func updateProxyStatusCaches(tx *gorm.DB, stats []domain.ProxyStatistic) error {
	if tx == nil {
		return fmt.Errorf("proxy status: database connection was not initialised")
	}

	latestEntries, proxyIDs := latestProxyStatusEntries(stats)
	if len(latestEntries) == 0 {
		return nil
	}

	if err := upsertProxyLatestStatistics(tx, latestEntries); err != nil {
		return err
	}

	if len(proxyIDs) == 0 {
		return nil
	}

	if err := upsertProxyOverallStatuses(tx, proxyIDs); err != nil {
		return err
	}
	if err := refreshUserProxyFilterIndexesForProxyIDs(tx, proxyIDs); err != nil {
		return err
	}
	return refreshUserScrapeSourceStatsForProxyIDs(tx, proxyIDs)
}

func latestProxyStatusEntries(stats []domain.ProxyStatistic) ([]domain.ProxyLatestStatistic, []uint64) {
	if len(stats) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	latest := make(map[proxyProtocolKey]domain.ProxyLatestStatistic, len(stats))
	proxyIDSet := make(map[uint64]struct{}, len(stats))

	for _, stat := range stats {
		if stat.ProxyID == 0 || stat.ProtocolID == 0 {
			continue
		}

		checkedAt := stat.CreatedAt
		if checkedAt.IsZero() {
			checkedAt = now
		}

		key := proxyProtocolKey{
			ProxyID:    stat.ProxyID,
			ProtocolID: stat.ProtocolID,
		}
		entry := domain.ProxyLatestStatistic{
			ProxyID:      stat.ProxyID,
			ProtocolID:   stat.ProtocolID,
			Alive:        stat.Alive,
			StatisticID:  stat.ID,
			ResponseTime: stat.ResponseTime,
			Attempt:      stat.Attempt,
			LevelID:      stat.LevelID,
			JudgeID:      stat.JudgeID,
			CheckedAt:    checkedAt,
		}

		if existing, ok := latest[key]; ok && !isNewerLatestStat(entry, existing) {
			continue
		}

		latest[key] = entry
		proxyIDSet[stat.ProxyID] = struct{}{}
	}

	if len(latest) == 0 {
		return nil, nil
	}

	entries := make([]domain.ProxyLatestStatistic, 0, len(latest))
	for _, entry := range latest {
		entries = append(entries, entry)
	}

	proxyIDs := make([]uint64, 0, len(proxyIDSet))
	for id := range proxyIDSet {
		proxyIDs = append(proxyIDs, id)
	}

	return entries, proxyIDs
}

func isNewerLatestStat(candidate domain.ProxyLatestStatistic, existing domain.ProxyLatestStatistic) bool {
	if candidate.CheckedAt.After(existing.CheckedAt) {
		return true
	}
	if candidate.CheckedAt.Before(existing.CheckedAt) {
		return false
	}
	return candidate.StatisticID > existing.StatisticID
}

func upsertProxyLatestStatistics(tx *gorm.DB, entries []domain.ProxyLatestStatistic) error {
	if len(entries) == 0 {
		return nil
	}

	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "proxy_id"},
			{Name: "protocol_id"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"alive":         gorm.Expr("excluded.alive"),
			"statistic_id":  gorm.Expr("excluded.statistic_id"),
			"response_time": gorm.Expr("excluded.response_time"),
			"attempt":       gorm.Expr("excluded.attempt"),
			"level_id":      gorm.Expr("excluded.level_id"),
			"judge_id":      gorm.Expr("excluded.judge_id"),
			"checked_at":    gorm.Expr("excluded.checked_at"),
			"updated_at":    gorm.Expr("CURRENT_TIMESTAMP"),
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{
				SQL: "excluded.checked_at > proxy_latest_statistics.checked_at OR " +
					"(excluded.checked_at = proxy_latest_statistics.checked_at AND " +
					"excluded.statistic_id > proxy_latest_statistics.statistic_id)",
			},
		}},
	}).Create(&entries).Error
}

func upsertProxyOverallStatuses(tx *gorm.DB, proxyIDs []uint64) error {
	if len(proxyIDs) == 0 {
		return nil
	}

	query := `
INSERT INTO proxy_overall_statuses (proxy_id, overall_alive, last_checked_at, created_at, updated_at)
SELECT
	pls.proxy_id,
	CASE WHEN MAX(CASE WHEN pls.alive THEN 1 ELSE 0 END) > 0 THEN TRUE ELSE FALSE END AS overall_alive,
	MAX(pls.checked_at) AS last_checked_at,
	CURRENT_TIMESTAMP,
	CURRENT_TIMESTAMP
FROM proxy_latest_statistics pls
WHERE pls.proxy_id IN ?
GROUP BY pls.proxy_id
ON CONFLICT (proxy_id) DO UPDATE
SET overall_alive = excluded.overall_alive,
	last_checked_at = excluded.last_checked_at,
	updated_at = CURRENT_TIMESTAMP;
`

	return tx.Exec(query, proxyIDs).Error
}

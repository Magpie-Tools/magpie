package database

import (
	"fmt"
	"sync"
	"time"

	"magpie/internal/domain"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type dashboardCheckCounts struct {
	TotalChecks     int64 `gorm:"column:total_checks"`
	TotalChecksWeek int64 `gorm:"column:total_checks_week"`
}

type dailyCheckAggregationKey struct {
	ProxyID uint64
	Day     time.Time
}

const proxyDailyBackfillAdvisoryLockBase int64 = 941_843_229_900
const proxyDailyBackfillBatchSize = 5000

var proxyDailyBackfillInFlight sync.Map

func incrementProxyDailyChecks(tx *gorm.DB, statistics []domain.ProxyStatistic) error {
	if tx == nil || len(statistics) == 0 {
		return nil
	}

	aggregated := make(map[dailyCheckAggregationKey]int64, len(statistics))
	for _, stat := range statistics {
		if stat.ProxyID == 0 {
			continue
		}

		checkTime := stat.CreatedAt
		if checkTime.IsZero() {
			checkTime = time.Now().UTC()
		}
		day := startOfUTCDay(checkTime)
		key := dailyCheckAggregationKey{
			ProxyID: stat.ProxyID,
			Day:     day,
		}
		aggregated[key]++
	}

	if len(aggregated) == 0 {
		return nil
	}

	entries := make([]domain.ProxyDailyCheck, 0, len(aggregated))
	for key, count := range aggregated {
		entries = append(entries, domain.ProxyDailyCheck{
			ProxyID:     key.ProxyID,
			Day:         key.Day,
			ChecksCount: count,
		})
	}

	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "proxy_id"},
			{Name: "day"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"checks_count": gorm.Expr("proxy_daily_checks.checks_count + EXCLUDED.checks_count"),
			"updated_at":   gorm.Expr("CURRENT_TIMESTAMP"),
		}),
	}).Create(&entries).Error; err != nil {
		return fmt.Errorf("daily checks: upsert counters: %w", err)
	}

	return nil
}

func loadDashboardCheckCounts(userID uint, weekAgo time.Time) (dashboardCheckCounts, error) {
	if DB == nil || userID == 0 {
		return dashboardCheckCounts{}, nil
	}

	queueProxyDailyChecksBackfill(userID)

	return queryDashboardCheckCountsFromDaily(userID, weekAgo)
}

func queueProxyDailyChecksBackfill(userID uint) {
	if DB == nil || userID == 0 {
		return
	}

	if _, loaded := proxyDailyBackfillInFlight.LoadOrStore(userID, struct{}{}); loaded {
		return
	}

	go func() {
		defer proxyDailyBackfillInFlight.Delete(userID)

		if err := ensureProxyDailyChecksBackfilled(userID); err != nil {
			log.Warn("dashboard: async daily check backfill failed", "user_id", userID, "error", err)
		}
	}()
}

func ensureProxyDailyChecksBackfilled(userID uint) error {
	if DB == nil || userID == 0 {
		return nil
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			lockKey := proxyDailyBackfillAdvisoryLockBase + int64(userID)
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockKey).Error; err != nil {
				return fmt.Errorf("daily checks: acquire backfill lock: %w", err)
			}
		}

		missingProxyIDs, err := loadUserProxyIDsMissingBackfill(tx, userID)
		if err != nil {
			return err
		}
		if len(missingProxyIDs) == 0 {
			return nil
		}

		if err := backfillProxyDailyChecksForProxyIDsTx(tx, missingProxyIDs); err != nil {
			return err
		}

		if err := markProxyDailyChecksBackfilled(tx, missingProxyIDs); err != nil {
			return err
		}

		return nil
	})
}

func loadUserProxyIDsMissingBackfill(tx *gorm.DB, userID uint) ([]uint64, error) {
	var rows []struct {
		ProxyID uint64 `gorm:"column:proxy_id"`
	}

	if err := tx.Table("user_proxies up").
		Select("up.proxy_id").
		Joins("LEFT JOIN proxy_daily_check_proxy_backfills b ON b.proxy_id = up.proxy_id").
		Where("up.user_id = ? AND b.proxy_id IS NULL", userID).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("daily checks: load missing proxy backfills: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	proxyIDs := make([]uint64, 0, len(rows))
	seen := make(map[uint64]struct{}, len(rows))
	for _, row := range rows {
		if row.ProxyID == 0 {
			continue
		}
		if _, exists := seen[row.ProxyID]; exists {
			continue
		}
		seen[row.ProxyID] = struct{}{}
		proxyIDs = append(proxyIDs, row.ProxyID)
	}

	return proxyIDs, nil
}

func markProxyDailyChecksBackfilled(tx *gorm.DB, proxyIDs []uint64) error {
	if tx == nil || len(proxyIDs) == 0 {
		return nil
	}

	entries := make([]domain.ProxyDailyCheckProxyBackfill, 0, len(proxyIDs))
	for _, proxyID := range proxyIDs {
		if proxyID == 0 {
			continue
		}
		entries = append(entries, domain.ProxyDailyCheckProxyBackfill{ProxyID: proxyID})
	}

	if len(entries) == 0 {
		return nil
	}

	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(entries, 1000).Error; err != nil {
		return fmt.Errorf("daily checks: persist proxy backfill markers: %w", err)
	}

	return nil
}

func queryDashboardCheckCountsFromDaily(userID uint, weekAgo time.Time) (dashboardCheckCounts, error) {
	var counts dashboardCheckCounts
	err := DB.Table("proxy_daily_checks pdc").
		Select("COALESCE(SUM(pdc.checks_count), 0) AS total_checks").
		Joins("JOIN user_proxies up ON up.proxy_id = pdc.proxy_id AND up.user_id = ?", userID).
		Scan(&counts).Error
	if err != nil {
		return dashboardCheckCounts{}, err
	}

	cutoff := weekAgo.UTC()
	cutoffDayStart := startOfUTCDay(cutoff)
	nextDayStart := cutoffDayStart.Add(24 * time.Hour)

	var fullDayWeek int64
	err = DB.Table("proxy_daily_checks pdc").
		Select(
			"COALESCE(SUM(pdc.checks_count), 0)",
		).
		Joins("JOIN user_proxies up ON up.proxy_id = pdc.proxy_id AND up.user_id = ?", userID).
		Where("pdc.day >= ?", nextDayStart).
		Scan(&fullDayWeek).Error
	if err != nil {
		return dashboardCheckCounts{}, err
	}

	var partialCutoffDay int64
	err = DB.Table("proxy_statistics ps").
		Select("COUNT(*)").
		Joins("JOIN user_proxies up ON up.proxy_id = ps.proxy_id AND up.user_id = ?", userID).
		Where("ps.created_at >= ? AND ps.created_at < ?", cutoff, nextDayStart).
		Scan(&partialCutoffDay).Error
	if err != nil {
		return dashboardCheckCounts{}, err
	}

	counts.TotalChecksWeek = fullDayWeek + partialCutoffDay
	return counts, nil
}

func loadDashboardCheckCountsDirect(userID uint, weekAgo time.Time) (dashboardCheckCounts, error) {
	var counts dashboardCheckCounts
	if DB == nil || userID == 0 {
		return counts, nil
	}

	err := DB.Model(&domain.ProxyStatistic{}).
		Select(
			"COUNT(*) AS total_checks, "+
				"COALESCE(SUM(CASE WHEN proxy_statistics.created_at >= ? THEN 1 ELSE 0 END), 0) AS total_checks_week",
			weekAgo,
		).
		Joins("JOIN user_proxies up ON up.proxy_id = proxy_statistics.proxy_id").
		Where("up.user_id = ?", userID).
		Scan(&counts).Error
	if err != nil {
		return dashboardCheckCounts{}, err
	}
	return counts, nil
}

func backfillProxyDailyChecksForProxyIDsTx(tx *gorm.DB, proxyIDs []uint64) error {
	if tx == nil || len(proxyIDs) == 0 {
		return nil
	}

	query := `
INSERT INTO proxy_daily_checks (proxy_id, day, checks_count, created_at, updated_at)
SELECT
	ps.proxy_id,
	DATE(ps.created_at AT TIME ZONE 'UTC') AS day,
	COUNT(*) AS checks_count,
	CURRENT_TIMESTAMP,
	CURRENT_TIMESTAMP
FROM proxy_statistics ps
WHERE ps.proxy_id IN ?
GROUP BY ps.proxy_id, DATE(ps.created_at AT TIME ZONE 'UTC')
ON CONFLICT (proxy_id, day) DO UPDATE
SET checks_count = GREATEST(proxy_daily_checks.checks_count, EXCLUDED.checks_count),
	updated_at = CURRENT_TIMESTAMP;
`

	for start := 0; start < len(proxyIDs); start += proxyDailyBackfillBatchSize {
		end := start + proxyDailyBackfillBatchSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}
		if err := tx.Exec(query, proxyIDs[start:end]).Error; err != nil {
			return fmt.Errorf("daily checks: backfill proxy daily counts: %w", err)
		}
	}

	return nil
}

func startOfUTCDay(value time.Time) time.Time {
	value = value.UTC()
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

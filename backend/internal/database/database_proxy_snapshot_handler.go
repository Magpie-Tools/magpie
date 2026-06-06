package database

import (
	"context"
	"fmt"
	"time"

	"magpie/internal/api/dto"
	"magpie/internal/domain"

	"gorm.io/gorm"
)

const (
	defaultProxySnapshotLimit = 96
	maxProxySnapshotLimit     = 720
)

// SaveProxySnapshots stores snapshots for alive and scraped proxies per user.
func SaveProxySnapshots(ctx context.Context) error {
	if DB == nil {
		return fmt.Errorf("database: connection was not configured")
	}

	tx := DB
	if ctx != nil {
		tx = tx.WithContext(ctx)
	}

	var userIDs []uint
	if err := tx.Model(&domain.User{}).Pluck("id", &userIDs).Error; err != nil {
		return fmt.Errorf("proxy snapshot: fetch user ids: %w", err)
	}

	if len(userIDs) == 0 {
		return nil
	}

	aliveCountByUser, err := aliveProxyCountByUser(tx, userIDs)
	if err != nil {
		return err
	}

	scrapedCountByUser, err := scrapedProxyCountByUser(tx, userIDs)
	if err != nil {
		return err
	}

	snapshots := make([]domain.ProxySnapshot, 0, len(userIDs)*2)
	for _, userID := range userIDs {
		snapshots = append(snapshots,
			domain.ProxySnapshot{
				UserID: userID,
				Metric: domain.ProxySnapshotMetricAlive,
				Count:  aliveCountByUser[userID],
			},
			domain.ProxySnapshot{
				UserID: userID,
				Metric: domain.ProxySnapshotMetricScraped,
				Count:  scrapedCountByUser[userID],
			},
		)
	}

	if len(snapshots) == 0 {
		return nil
	}

	if err := tx.Create(&snapshots).Error; err != nil {
		return fmt.Errorf("proxy snapshot: insert rows: %w", err)
	}

	return nil
}

// GetProxySnapshotEntries returns the most recent proxy snapshot entries for a user/metric combination.
func GetProxySnapshotEntries(userID uint, metric string, limit int) []dto.ProxySnapshotEntry {
	if DB == nil {
		return nil
	}

	if metric != domain.ProxySnapshotMetricAlive && metric != domain.ProxySnapshotMetricScraped {
		return nil
	}

	limit = normalizeProxySnapshotLimit(limit)

	rows := make([]domain.ProxySnapshot, 0, limit)

	DB.Where("user_id = ? AND metric = ?", userID, metric).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows)

	if len(rows) == 0 {
		return nil
	}

	entries := make([]dto.ProxySnapshotEntry, len(rows))
	for index := range rows {
		row := rows[len(rows)-1-index]
		entries[index] = dto.ProxySnapshotEntry{
			Count:      row.Count,
			RecordedAt: row.CreatedAt,
		}
	}

	return entries
}

func normalizeProxySnapshotLimit(limit int) int {
	if limit <= 0 {
		return defaultProxySnapshotLimit
	}
	if limit > maxProxySnapshotLimit {
		return maxProxySnapshotLimit
	}
	return limit
}

func aliveProxyCountByUser(tx *gorm.DB, userIDs []uint) (map[uint]int64, error) {
	var rows []struct {
		UserID     uint
		AliveCount int64
	}

	if err := tx.Table("user_proxies AS up").
		Select("up.user_id AS user_id, COUNT(DISTINCT up.proxy_id) AS alive_count").
		Joins("JOIN proxy_overall_statuses pos ON pos.proxy_id = up.proxy_id").
		Where("up.user_id IN ?", userIDs).
		Where("pos.overall_alive = ?", true).
		Group("up.user_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("proxy snapshot: aggregate alive counts: %w", err)
	}

	counts := make(map[uint]int64, len(userIDs))
	for _, userID := range userIDs {
		counts[userID] = 0
	}

	for _, row := range rows {
		counts[row.UserID] = row.AliveCount
	}

	return counts, nil
}

func scrapedProxyCountByUser(tx *gorm.DB, userIDs []uint) (map[uint]int64, error) {
	var rows []struct {
		UserID       uint
		ScrapedCount int64
	}

	if err := tx.Table("user_proxies AS up").
		Select("up.user_id AS user_id, COUNT(*) AS scraped_count").
		Where("up.user_id IN ?", userIDs).
		Where("EXISTS (SELECT 1 FROM proxy_scrape_site ps WHERE ps.proxy_id = up.proxy_id)").
		Group("up.user_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("proxy snapshot: aggregate scraped counts: %w", err)
	}

	counts := make(map[uint]int64, len(userIDs))
	for _, userID := range userIDs {
		counts[userID] = 0
	}

	for _, row := range rows {
		counts[row.UserID] = row.ScrapedCount
	}

	return counts, nil
}

// GetCurrentAliveProxyCount returns the latest alive proxy count for a user based on proxy statistics.
func GetCurrentAliveProxyCount(userID uint) int64 {
	if DB == nil {
		return 0
	}

	counts, err := aliveProxyCountByUser(DB, []uint{userID})
	if err != nil {
		return 0
	}

	return counts[userID]
}

func GetCurrentScrapedProxyCount(userID uint) int64 {
	if DB == nil {
		return 0
	}

	counts, err := scrapedProxyCountByUser(DB, []uint{userID})
	if err != nil {
		return 0
	}

	return counts[userID]
}

type proxySnapshotCountSummary struct {
	Current  int64
	Increase int64
	Found    bool
}

func getProxySnapshotCountSummary(userID uint, metric string, since time.Time) proxySnapshotCountSummary {
	if DB == nil || userID == 0 {
		return proxySnapshotCountSummary{}
	}

	var latest domain.ProxySnapshot
	latestResult := DB.
		Where("user_id = ? AND metric = ?", userID, metric).
		Order("created_at DESC, id DESC").
		Limit(1).
		Find(&latest)
	if latestResult.Error != nil || latestResult.RowsAffected == 0 {
		return proxySnapshotCountSummary{}
	}

	var baseline domain.ProxySnapshot
	baselineResult := DB.
		Where("user_id = ? AND metric = ? AND created_at >= ?", userID, metric, since).
		Order("created_at ASC, id ASC").
		Limit(1).
		Find(&baseline)

	increase := int64(0)
	if baselineResult.Error == nil && baselineResult.RowsAffected > 0 && latest.Count > baseline.Count {
		increase = latest.Count - baseline.Count
	}

	return proxySnapshotCountSummary{
		Current:  latest.Count,
		Increase: increase,
		Found:    true,
	}
}

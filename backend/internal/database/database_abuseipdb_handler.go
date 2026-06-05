package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"magpie/internal/abuseipdb"
	"magpie/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func GetNextAbuseIPDBProxyToCheck(ctx context.Context, staleBefore time.Time) (*domain.Proxy, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialised")
	}

	var proxy domain.Proxy
	err := DB.WithContext(ctx).
		Model(&domain.Proxy{}).
		Where("EXISTS (SELECT 1 FROM user_proxies up WHERE up.proxy_id = proxies.id)").
		Joins("LEFT JOIN abuseipdb_checks ac ON ac.proxy_id = proxies.id").
		Where("ac.checked_at IS NULL OR ac.checked_at < ?", staleBefore).
		Order("ac.checked_at ASC NULLS FIRST, proxies.id ASC").
		Limit(1).
		First(&proxy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &proxy, nil
}

func UpsertAbuseIPDBCheck(ctx context.Context, proxyID uint64, result abuseipdb.CheckResult, checkedAt time.Time) error {
	if DB == nil {
		return fmt.Errorf("database not initialised")
	}
	if proxyID == 0 {
		return fmt.Errorf("proxy id is required")
	}
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}

	row := domain.AbuseIPDBCheck{
		ProxyID:              proxyID,
		AbuseConfidenceScore: result.AbuseConfidenceScore,
		TotalReports:         result.TotalReports,
		NumDistinctUsers:     result.NumDistinctUsers,
		UsageType:            result.UsageType,
		ISP:                  result.ISP,
		Domain:               result.Domain,
		CountryCode:          result.CountryCode,
		IsWhitelisted:        result.IsWhitelisted,
		IsTor:                result.IsTor,
		LastReportedAt:       result.LastReportedAt,
		CheckedAt:            checkedAt.UTC(),
	}

	return DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "proxy_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"abuse_confidence_score",
			"total_reports",
			"num_distinct_users",
			"usage_type",
			"isp",
			"domain",
			"country_code",
			"is_whitelisted",
			"is_tor",
			"last_reported_at",
			"checked_at",
			"updated_at",
		}),
	}).Create(&row).Error
}

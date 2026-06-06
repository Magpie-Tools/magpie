package database

import (
	"fmt"

	"magpie/internal/domain"

	"gorm.io/gorm"
)

func ensureProxyListPerformanceSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("nil database connection")
	}
	if !isPostgresDialect(db) {
		return nil
	}

	if db.Migrator().HasTable(&domain.ProxyLatestStatistic{}) {
		stmts := []string{
			`ALTER TABLE proxy_latest_statistics ADD COLUMN IF NOT EXISTS response_time integer`,
			`ALTER TABLE proxy_latest_statistics ADD COLUMN IF NOT EXISTS attempt smallint`,
			`ALTER TABLE proxy_latest_statistics ADD COLUMN IF NOT EXISTS level_id bigint`,
			`UPDATE proxy_latest_statistics AS pls
			 SET response_time = ps.response_time,
			     attempt = ps.attempt,
			     level_id = ps.level_id
			 FROM proxy_statistics AS ps
			 WHERE ps.id = pls.statistic_id
			   AND ps.created_at = pls.checked_at
			   AND (pls.response_time IS NULL OR pls.attempt IS NULL)`,
		}
		for _, stmt := range stmts {
			if err := db.Exec(stmt).Error; err != nil {
				return fmt.Errorf("proxy list latest-statistic schema: %w", err)
			}
		}
		if err := db.Exec(`
			CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_proxy_latest_statistics_proxy_checked_stat
			ON proxy_latest_statistics (proxy_id, checked_at DESC, statistic_id DESC)
		`).Error; err != nil {
			return fmt.Errorf("proxy list latest-statistic index schema: %w", err)
		}
	}

	if db.Migrator().HasTable(&domain.ProxyScrapeSite{}) {
		if err := db.Exec(`
			CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_proxy_scrape_site_scrape_proxy
			ON proxy_scrape_site (scrape_site_id, proxy_id)
		`).Error; err != nil {
			return fmt.Errorf("proxy list scrape-source index schema: %w", err)
		}
	}

	return nil
}

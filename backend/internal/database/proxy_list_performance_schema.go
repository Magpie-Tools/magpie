package database

import (
	"context"
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
			`ALTER TABLE proxy_latest_statistics ADD COLUMN IF NOT EXISTS judge_id bigint`,
			`UPDATE proxy_latest_statistics AS pls
			 SET response_time = ps.response_time,
			     attempt = ps.attempt,
			     level_id = ps.level_id,
			     judge_id = ps.judge_id
			 FROM proxy_statistics AS ps
			 WHERE ps.id = pls.statistic_id
			   AND ps.created_at = pls.checked_at
			   AND (
			       pls.response_time IS NULL OR
			       pls.attempt IS NULL OR
			       pls.judge_id IS NULL
			   )`,
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
		if err := ensureProxyScrapeSiteCascadeConstraints(db); err != nil {
			return err
		}
		if err := db.Exec(`
			CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_proxy_scrape_site_scrape_proxy
			ON proxy_scrape_site (scrape_site_id, proxy_id)
		`).Error; err != nil {
			return fmt.Errorf("proxy list scrape-source index schema: %w", err)
		}
	}

	return nil
}

func ensureProxyScrapeSiteCascadeConstraints(db *gorm.DB) error {
	const constraintsSQL = `
DO $$
DECLARE
	constraint_row RECORD;
BEGIN
	FOR constraint_row IN
		SELECT c.conname
		FROM pg_constraint c
		JOIN pg_attribute a
		  ON a.attrelid = c.conrelid
		 AND a.attnum = c.conkey[1]
		WHERE c.conrelid = 'proxy_scrape_site'::regclass
		  AND c.contype = 'f'
		  AND cardinality(c.conkey) = 1
		  AND a.attname = 'proxy_id'
		  AND NOT (
			  c.confrelid = 'proxies'::regclass
			  AND c.confdeltype = 'c'
			  AND c.confupdtype = 'c'
		  )
	LOOP
		EXECUTE format(
			'ALTER TABLE proxy_scrape_site DROP CONSTRAINT %I',
			constraint_row.conname
		);
	END LOOP;

	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint c
		JOIN pg_attribute a
		  ON a.attrelid = c.conrelid
		 AND a.attnum = c.conkey[1]
		WHERE c.conrelid = 'proxy_scrape_site'::regclass
		  AND c.contype = 'f'
		  AND cardinality(c.conkey) = 1
		  AND a.attname = 'proxy_id'
		  AND c.confrelid = 'proxies'::regclass
		  AND c.confdeltype = 'c'
		  AND c.confupdtype = 'c'
	) THEN
		ALTER TABLE proxy_scrape_site
			ADD CONSTRAINT fk_proxy_scrape_site_proxy_cascade
			FOREIGN KEY (proxy_id)
			REFERENCES proxies(id)
			ON UPDATE CASCADE
			ON DELETE CASCADE
			NOT VALID;
	END IF;

	FOR constraint_row IN
		SELECT c.conname
		FROM pg_constraint c
		JOIN pg_attribute a
		  ON a.attrelid = c.conrelid
		 AND a.attnum = c.conkey[1]
		WHERE c.conrelid = 'proxy_scrape_site'::regclass
		  AND c.contype = 'f'
		  AND cardinality(c.conkey) = 1
		  AND a.attname = 'scrape_site_id'
		  AND NOT (
			  c.confrelid = 'scrape_sites'::regclass
			  AND c.confdeltype = 'c'
			  AND c.confupdtype = 'c'
		  )
	LOOP
		EXECUTE format(
			'ALTER TABLE proxy_scrape_site DROP CONSTRAINT %I',
			constraint_row.conname
		);
	END LOOP;

	IF NOT EXISTS (
		SELECT 1
		FROM pg_constraint c
		JOIN pg_attribute a
		  ON a.attrelid = c.conrelid
		 AND a.attnum = c.conkey[1]
		WHERE c.conrelid = 'proxy_scrape_site'::regclass
		  AND c.contype = 'f'
		  AND cardinality(c.conkey) = 1
		  AND a.attname = 'scrape_site_id'
		  AND c.confrelid = 'scrape_sites'::regclass
		  AND c.confdeltype = 'c'
		  AND c.confupdtype = 'c'
	) THEN
		ALTER TABLE proxy_scrape_site
			ADD CONSTRAINT fk_proxy_scrape_site_source_cascade
			FOREIGN KEY (scrape_site_id)
			REFERENCES scrape_sites(id)
			ON UPDATE CASCADE
			ON DELETE CASCADE
			NOT VALID;
	END IF;
END $$;
`

	if err := db.Exec(constraintsSQL).Error; err != nil {
		return fmt.Errorf("proxy scrape-source cascade constraints: %w", err)
	}
	return nil
}

func ValidateProxyScrapeSiteCascadeConstraints(ctx context.Context) error {
	if DB == nil {
		return fmt.Errorf("database not initialised")
	}

	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}
	if !isPostgresDialect(db) {
		return nil
	}

	const validationSQL = `
DO $$
DECLARE
	constraint_row RECORD;
BEGIN
	FOR constraint_row IN
		SELECT conname
		FROM pg_constraint
		WHERE conrelid = 'proxy_scrape_site'::regclass
		  AND contype = 'f'
		  AND confdeltype = 'c'
		  AND NOT convalidated
	LOOP
		EXECUTE format(
			'ALTER TABLE proxy_scrape_site VALIDATE CONSTRAINT %I',
			constraint_row.conname
		);
	END LOOP;
END $$;
`

	if err := db.Exec(validationSQL).Error; err != nil {
		return fmt.Errorf("validate proxy scrape-source cascade constraints: %w", err)
	}
	return nil
}

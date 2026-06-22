package database

import (
	"context"
	"fmt"
	"sync"
	"time"

	"magpie/internal/domain"
	"magpie/internal/support"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

const (
	defaultReadModelRefreshIntervalSeconds = 5
	defaultReadModelRefreshBatchSize       = 5000
	defaultReadModelRefreshMaxBatches      = 4
	envReadModelRefreshIntervalSeconds     = "READ_MODEL_REFRESH_INTERVAL_SECONDS"
	envReadModelRefreshBatchSize           = "READ_MODEL_REFRESH_BATCH_SIZE"
	envReadModelRefreshMaxBatches          = "READ_MODEL_REFRESH_MAX_BATCHES"
)

var (
	readModelDirtyMu       sync.Mutex
	readModelDirtyProxyIDs = make(map[uint64]struct{})
)

func ensureReadModelBackfill(db *gorm.DB) error {
	if db == nil || !isPostgresDialect(db) {
		return nil
	}

	if err := ensureReadModelSchema(db); err != nil {
		return err
	}

	if db.Migrator().HasTable(&domain.UserProxyFilterIndex{}) {
		var count int64
		if err := db.Model(&domain.UserProxyFilterIndex{}).Count(&count).Error; err != nil {
			return fmt.Errorf("read model: count proxy filter index: %w", err)
		}
		if count == 0 {
			if err := refreshAllUserProxyFilterIndexes(db); err != nil {
				return err
			}
			log.Info("Proxy filter read model backfilled")
		}
	}

	if db.Migrator().HasTable(&domain.UserScrapeSourceStat{}) {
		var count int64
		if err := db.Model(&domain.UserScrapeSourceStat{}).Count(&count).Error; err != nil {
			return fmt.Errorf("read model: count scrape-source stats: %w", err)
		}
		if count == 0 {
			if err := refreshAllUserScrapeSourceStats(db); err != nil {
				return err
			}
			log.Info("Scrape-source stats read model backfilled")
		}
	}

	return nil
}

func ensureReadModelSchema(db *gorm.DB) error {
	if db == nil || !isPostgresDialect(db) {
		return nil
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_proxy_filter_indexes (
			user_id bigint NOT NULL,
			proxy_id bigint NOT NULL,
			PRIMARY KEY (user_id, proxy_id)
		)`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS ip text NOT NULL DEFAULT ''`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS ip_int bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS port integer NOT NULL DEFAULT 0`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS country varchar(56) NOT NULL DEFAULT 'N/A'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS country_key varchar(56) NOT NULL DEFAULT 'n/a'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS estimated_type varchar(20) NOT NULL DEFAULT 'N/A'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS type_key varchar(20) NOT NULL DEFAULT 'n/a'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS anonymity_level varchar(50) NOT NULL DEFAULT 'N/A'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS anonymity_key varchar(50) NOT NULL DEFAULT 'n/a'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS alive boolean NOT NULL DEFAULT false`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS latest_check timestamptz NOT NULL DEFAULT '0001-01-01 00:00:00+00'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS response_time integer NOT NULL DEFAULT 0`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS attempt integer NOT NULL DEFAULT 0`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS health_overall real`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS health_http real`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS health_https real`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS health_socks4 real`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS health_socks5 real`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS alive_http boolean NOT NULL DEFAULT false`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS alive_https boolean NOT NULL DEFAULT false`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS alive_socks4 boolean NOT NULL DEFAULT false`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS alive_socks5 boolean NOT NULL DEFAULT false`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS reputation_label varchar(16) NOT NULL DEFAULT 'unknown'`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS reputation_score real`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE user_proxy_filter_indexes ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_user_alive_latest ON user_proxy_filter_indexes (user_id, alive, latest_check DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_user_country ON user_proxy_filter_indexes (user_id, country_key)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_user_type ON user_proxy_filter_indexes (user_id, type_key)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_user_reputation ON user_proxy_filter_indexes (user_id, reputation_label, reputation_score DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_proxy_id ON user_proxy_filter_indexes (proxy_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_ip_int ON user_proxy_filter_indexes (ip_int)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_port ON user_proxy_filter_indexes (port)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_anonymity_key ON user_proxy_filter_indexes (anonymity_key)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_alive_http ON user_proxy_filter_indexes (alive_http)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_alive_https ON user_proxy_filter_indexes (alive_https)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_alive_socks4 ON user_proxy_filter_indexes (alive_socks4)`,
		`CREATE INDEX IF NOT EXISTS idx_user_proxy_filter_alive_socks5 ON user_proxy_filter_indexes (alive_socks5)`,
		`CREATE TABLE IF NOT EXISTS user_scrape_source_stats (
			user_id bigint NOT NULL,
			scrape_site_id bigint NOT NULL,
			PRIMARY KEY (user_id, scrape_site_id)
		)`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS url text NOT NULL DEFAULT ''`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS protocol_key varchar(16) NOT NULL DEFAULT ''`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS proxy_count bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS alive_count bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS dead_count bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS unknown_count bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS added_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE user_scrape_source_stats ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP`,
		`CREATE INDEX IF NOT EXISTS idx_user_scrape_source_stats_user_added ON user_scrape_source_stats (user_id, added_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_user_scrape_source_stats_user_protocol ON user_scrape_source_stats (user_id, protocol_key)`,
		`CREATE INDEX IF NOT EXISTS idx_user_scrape_source_stats_proxy_count ON user_scrape_source_stats (proxy_count)`,
		`CREATE INDEX IF NOT EXISTS idx_user_scrape_source_stats_alive_count ON user_scrape_source_stats (alive_count)`,
	}

	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("read model schema: %w", err)
		}
	}

	return nil
}

func QueueReadModelRefreshForProxyIDs(proxyIDs []uint64) {
	if len(proxyIDs) == 0 {
		return
	}

	readModelDirtyMu.Lock()
	for _, proxyID := range proxyIDs {
		if proxyID != 0 {
			readModelDirtyProxyIDs[proxyID] = struct{}{}
		}
	}
	readModelDirtyMu.Unlock()

}

func StartReadModelRefreshRoutine(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	interval := readModelRefreshInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			flushReadModelDirtyProxyIDs(context.Background(), true)
			return
		case <-ticker.C:
			flushReadModelDirtyProxyIDs(ctx, false)
		}
	}
}

func flushReadModelDirtyProxyIDs(ctx context.Context, drainAll bool) {
	if DB == nil {
		return
	}

	maxBatches := readModelRefreshMaxBatches()
	if drainAll {
		maxBatches = 0
	}

	processed := 0
	for {
		if !drainAll && processed >= maxBatches {
			return
		}

		proxyIDs := popDirtyReadModelProxyIDs(readModelRefreshBatchSize())
		if len(proxyIDs) == 0 {
			return
		}

		db := DB
		if ctx != nil {
			db = db.WithContext(ctx)
		}

		start := time.Now()
		if err := refreshUserProxyFilterIndexesForProxyIDs(db, proxyIDs); err != nil {
			requeueDirtyReadModelProxyIDs(proxyIDs)
			log.Warn("read model refresh: proxy filter index failed", "error", err, "count", len(proxyIDs))
			return
		}
		if err := refreshUserScrapeSourceStatsForProxyIDs(db, proxyIDs); err != nil {
			requeueDirtyReadModelProxyIDs(proxyIDs)
			log.Warn("read model refresh: scrape-source stats failed", "error", err, "count", len(proxyIDs))
			return
		}

		log.Debug("Read models refreshed", "proxy_count", len(proxyIDs), "duration", time.Since(start))
		processed++
	}
}

func popDirtyReadModelProxyIDs(limit int) []uint64 {
	if limit <= 0 {
		limit = defaultReadModelRefreshBatchSize
	}

	readModelDirtyMu.Lock()
	defer readModelDirtyMu.Unlock()

	if len(readModelDirtyProxyIDs) == 0 {
		return nil
	}

	proxyIDs := make([]uint64, 0, min(limit, len(readModelDirtyProxyIDs)))
	for proxyID := range readModelDirtyProxyIDs {
		proxyIDs = append(proxyIDs, proxyID)
		delete(readModelDirtyProxyIDs, proxyID)
		if len(proxyIDs) >= limit {
			break
		}
	}
	return proxyIDs
}

func requeueDirtyReadModelProxyIDs(proxyIDs []uint64) {
	if len(proxyIDs) == 0 {
		return
	}

	readModelDirtyMu.Lock()
	for _, proxyID := range proxyIDs {
		if proxyID != 0 {
			readModelDirtyProxyIDs[proxyID] = struct{}{}
		}
	}
	readModelDirtyMu.Unlock()
}

func readModelRefreshInterval() time.Duration {
	seconds := support.GetEnvInt(envReadModelRefreshIntervalSeconds, defaultReadModelRefreshIntervalSeconds)
	if seconds <= 0 {
		seconds = defaultReadModelRefreshIntervalSeconds
	}
	return time.Duration(seconds) * time.Second
}

func readModelRefreshBatchSize() int {
	size := support.GetEnvInt(envReadModelRefreshBatchSize, defaultReadModelRefreshBatchSize)
	if size <= 0 {
		return defaultReadModelRefreshBatchSize
	}
	return size
}

func readModelRefreshMaxBatches() int {
	maxBatches := support.GetEnvInt(envReadModelRefreshMaxBatches, defaultReadModelRefreshMaxBatches)
	if maxBatches <= 0 {
		return defaultReadModelRefreshMaxBatches
	}
	return maxBatches
}

func refreshAllUserProxyFilterIndexes(db *gorm.DB) error {
	return refreshUserProxyFilterIndexes(db, "")
}

func refreshUserProxyFilterIndexesForProxyIDs(tx *gorm.DB, proxyIDs []uint64) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	if tx == nil {
		return nil
	}
	for start := 0; start < len(proxyIDs); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}
		if err := refreshUserProxyFilterIndexes(tx, "WHERE up.proxy_id IN ?", proxyIDs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func refreshUserProxyFilterIndexesForUserProxyIDs(tx *gorm.DB, userID uint, proxyIDs []uint64) error {
	if userID == 0 || len(proxyIDs) == 0 {
		return nil
	}
	if tx == nil {
		return nil
	}
	for start := 0; start < len(proxyIDs); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}
		if err := refreshUserProxyFilterIndexes(tx, "WHERE up.user_id = ? AND up.proxy_id IN ?", userID, proxyIDs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func refreshUserProxyFilterIndexes(tx *gorm.DB, where string, args ...interface{}) error {
	if tx == nil || !tx.Migrator().HasTable(&domain.UserProxyFilterIndex{}) {
		return nil
	}

	query := `
WITH scope AS (
	SELECT up.user_id, up.proxy_id
	FROM user_proxies up
	` + where + `
),
latest AS (
	SELECT proxy_id, level_id, response_time, attempt, checked_at
	FROM (
		SELECT
			pls.proxy_id,
			pls.level_id,
			COALESCE(pls.response_time, 0) AS response_time,
			COALESCE(pls.attempt, 0) AS attempt,
			pls.checked_at,
			ROW_NUMBER() OVER (PARTITION BY pls.proxy_id ORDER BY pls.checked_at DESC, pls.statistic_id DESC) AS row_num
		FROM proxy_latest_statistics pls
		WHERE EXISTS (SELECT 1 FROM scope s WHERE s.proxy_id = pls.proxy_id)
	) ranked
	WHERE row_num = 1
),
health AS (
	SELECT
		pls.proxy_id,
		ROUND(100.0 * AVG(CASE WHEN pls.alive THEN 1 ELSE 0 END)::numeric, 1) AS health_overall,
		MAX(CASE WHEN LOWER(proto.name) = 'http' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_http,
		MAX(CASE WHEN LOWER(proto.name) = 'https' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_https,
		MAX(CASE WHEN LOWER(proto.name) = 'socks4' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_socks4,
		MAX(CASE WHEN LOWER(proto.name) = 'socks5' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_socks5,
		BOOL_OR(pls.alive AND LOWER(proto.name) = 'http') AS alive_http,
		BOOL_OR(pls.alive AND LOWER(proto.name) = 'https') AS alive_https,
		BOOL_OR(pls.alive AND LOWER(proto.name) = 'socks4') AS alive_socks4,
		BOOL_OR(pls.alive AND LOWER(proto.name) = 'socks5') AS alive_socks5
	FROM proxy_latest_statistics pls
	JOIN protocols proto ON proto.id = pls.protocol_id
	WHERE EXISTS (SELECT 1 FROM scope s WHERE s.proxy_id = pls.proxy_id)
	GROUP BY pls.proxy_id
),
rows AS (
	SELECT
		up.user_id,
		p.id AS proxy_id,
		p.ip,
		p.ip_int,
		p.port,
		COALESCE(NULLIF(TRIM(p.country), ''), 'N/A') AS country,
		LOWER(COALESCE(NULLIF(TRIM(p.country), ''), 'n/a')) AS country_key,
		COALESCE(NULLIF(TRIM(p.estimated_type), ''), 'N/A') AS estimated_type,
		LOWER(COALESCE(NULLIF(TRIM(p.estimated_type), ''), 'n/a')) AS type_key,
		COALESCE(al.name, 'N/A') AS anonymity_level,
		LOWER(COALESCE(al.name, 'n/a')) AS anonymity_key,
		COALESCE(pos.overall_alive, FALSE) AS alive,
		COALESCE(pos.last_checked_at, latest.checked_at, '0001-01-01 00:00:00'::timestamp) AS latest_check,
		COALESCE(latest.response_time, 0) AS response_time,
		COALESCE(latest.attempt, 0) AS attempt,
		health.health_overall,
		health.health_http,
		health.health_https,
		health.health_socks4,
		health.health_socks5,
		COALESCE(health.alive_http, FALSE) AS alive_http,
		COALESCE(health.alive_https, FALSE) AS alive_https,
		COALESCE(health.alive_socks4, FALSE) AS alive_socks4,
		COALESCE(health.alive_socks5, FALSE) AS alive_socks5,
		LOWER(COALESCE(NULLIF(pr.label, ''), 'unknown')) AS reputation_label,
		pr.score AS reputation_score
	FROM scope up
	JOIN proxies p ON p.id = up.proxy_id
	LEFT JOIN latest ON latest.proxy_id = p.id
	LEFT JOIN proxy_overall_statuses pos ON pos.proxy_id = p.id
	LEFT JOIN anonymity_levels al ON al.id = latest.level_id
	LEFT JOIN health ON health.proxy_id = p.id
	LEFT JOIN proxy_reputations pr ON pr.proxy_id = p.id AND pr.kind = 'overall'
)
INSERT INTO user_proxy_filter_indexes (
	user_id, proxy_id, ip, ip_int, port,
	country, country_key, estimated_type, type_key, anonymity_level, anonymity_key,
	alive, latest_check, response_time, attempt,
	health_overall, health_http, health_https, health_socks4, health_socks5,
	alive_http, alive_https, alive_socks4, alive_socks5,
	reputation_label, reputation_score, created_at, updated_at
)
SELECT
	user_id, proxy_id, ip, ip_int, port,
	country, country_key, estimated_type, type_key, anonymity_level, anonymity_key,
	alive, latest_check, response_time, attempt,
	health_overall, health_http, health_https, health_socks4, health_socks5,
	alive_http, alive_https, alive_socks4, alive_socks5,
	reputation_label, reputation_score, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM rows
ON CONFLICT (user_id, proxy_id) DO UPDATE SET
	ip = EXCLUDED.ip,
	ip_int = EXCLUDED.ip_int,
	port = EXCLUDED.port,
	country = EXCLUDED.country,
	country_key = EXCLUDED.country_key,
	estimated_type = EXCLUDED.estimated_type,
	type_key = EXCLUDED.type_key,
	anonymity_level = EXCLUDED.anonymity_level,
	anonymity_key = EXCLUDED.anonymity_key,
	alive = EXCLUDED.alive,
	latest_check = EXCLUDED.latest_check,
	response_time = EXCLUDED.response_time,
	attempt = EXCLUDED.attempt,
	health_overall = EXCLUDED.health_overall,
	health_http = EXCLUDED.health_http,
	health_https = EXCLUDED.health_https,
	health_socks4 = EXCLUDED.health_socks4,
	health_socks5 = EXCLUDED.health_socks5,
	alive_http = EXCLUDED.alive_http,
	alive_https = EXCLUDED.alive_https,
	alive_socks4 = EXCLUDED.alive_socks4,
	alive_socks5 = EXCLUDED.alive_socks5,
	reputation_label = EXCLUDED.reputation_label,
	reputation_score = EXCLUDED.reputation_score,
	updated_at = CURRENT_TIMESTAMP
`
	if err := tx.Exec(query, args...).Error; err != nil {
		return fmt.Errorf("read model: refresh proxy filter index: %w", err)
	}
	return nil
}

func refreshAllUserScrapeSourceStats(db *gorm.DB) error {
	return refreshUserScrapeSourceStats(db, "")
}

func refreshUserScrapeSourceStatsForSites(tx *gorm.DB, siteIDs []uint64) error {
	if tx == nil || len(siteIDs) == 0 {
		return nil
	}
	for start := 0; start < len(siteIDs); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(siteIDs) {
			end = len(siteIDs)
		}
		if err := refreshUserScrapeSourceStats(tx, "WHERE ss.id IN ?", siteIDs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func refreshUserScrapeSourceStatsForUserSites(tx *gorm.DB, userID uint, siteIDs []uint64) error {
	if tx == nil || userID == 0 || len(siteIDs) == 0 {
		return nil
	}
	for start := 0; start < len(siteIDs); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(siteIDs) {
			end = len(siteIDs)
		}
		if err := refreshUserScrapeSourceStats(tx, "WHERE uss.user_id = ? AND ss.id IN ?", userID, siteIDs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func refreshUserScrapeSourceStatsForProxyIDs(tx *gorm.DB, proxyIDs []uint64) error {
	if tx == nil || len(proxyIDs) == 0 || !tx.Migrator().HasTable(&domain.UserScrapeSourceStat{}) {
		return nil
	}

	for start := 0; start < len(proxyIDs); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}

		var siteIDs []uint64
		if err := tx.Table("proxy_scrape_site").
			Distinct("scrape_site_id").
			Where("proxy_id IN ?", proxyIDs[start:end]).
			Pluck("scrape_site_id", &siteIDs).Error; err != nil {
			return fmt.Errorf("read model: load scrape-source ids for proxies: %w", err)
		}
		if err := refreshUserScrapeSourceStatsForSites(tx, siteIDs); err != nil {
			return err
		}
	}
	return nil
}

func refreshUserScrapeSourceStatsForUserProxyIDs(tx *gorm.DB, userID uint, proxyIDs []uint64) error {
	if tx == nil || userID == 0 || len(proxyIDs) == 0 || !tx.Migrator().HasTable(&domain.UserScrapeSourceStat{}) {
		return nil
	}

	for start := 0; start < len(proxyIDs); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}

		var siteIDs []uint64
		if err := tx.Table("proxy_scrape_site pss").
			Distinct("pss.scrape_site_id").
			Joins("JOIN user_scrape_site uss ON uss.scrape_site_id = pss.scrape_site_id AND uss.user_id = ?", userID).
			Where("pss.proxy_id IN ?", proxyIDs[start:end]).
			Pluck("pss.scrape_site_id", &siteIDs).Error; err != nil {
			return fmt.Errorf("read model: load user scrape-source ids for proxies: %w", err)
		}
		if err := refreshUserScrapeSourceStatsForUserSites(tx, userID, siteIDs); err != nil {
			return err
		}
	}
	return nil
}

func refreshUserScrapeSourceStats(tx *gorm.DB, where string, args ...interface{}) error {
	if tx == nil || !tx.Migrator().HasTable(&domain.UserScrapeSourceStat{}) {
		return nil
	}

	query := `
WITH rows AS (
	SELECT
		uss.user_id,
		ss.id AS scrape_site_id,
		ss.url,
		LOWER(split_part(ss.url, '://', 1)) AS protocol_key,
		COUNT(up.proxy_id) AS proxy_count,
		COALESCE(SUM(CASE WHEN pos.overall_alive IS TRUE THEN 1 ELSE 0 END), 0) AS alive_count,
		COALESCE(SUM(CASE WHEN pos.overall_alive IS FALSE THEN 1 ELSE 0 END), 0) AS dead_count,
		COALESCE(SUM(CASE WHEN up.proxy_id IS NOT NULL AND pos.proxy_id IS NULL THEN 1 ELSE 0 END), 0) AS unknown_count,
		uss.created_at AS added_at
	FROM user_scrape_site uss
	JOIN scrape_sites ss ON ss.id = uss.scrape_site_id
	LEFT JOIN proxy_scrape_site pss ON pss.scrape_site_id = ss.id
	LEFT JOIN user_proxies up ON up.user_id = uss.user_id AND up.proxy_id = pss.proxy_id
	LEFT JOIN proxy_overall_statuses pos ON pos.proxy_id = up.proxy_id
	` + where + `
	GROUP BY uss.user_id, ss.id, ss.url, uss.created_at
)
INSERT INTO user_scrape_source_stats (
	user_id, scrape_site_id, url, protocol_key,
	proxy_count, alive_count, dead_count, unknown_count,
	added_at, created_at, updated_at
)
SELECT
	user_id, scrape_site_id, url, protocol_key,
	proxy_count, alive_count, dead_count, unknown_count,
	added_at, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM rows
ON CONFLICT (user_id, scrape_site_id) DO UPDATE SET
	url = EXCLUDED.url,
	protocol_key = EXCLUDED.protocol_key,
	proxy_count = EXCLUDED.proxy_count,
	alive_count = EXCLUDED.alive_count,
	dead_count = EXCLUDED.dead_count,
	unknown_count = EXCLUDED.unknown_count,
	added_at = EXCLUDED.added_at,
	updated_at = CURRENT_TIMESTAMP
`
	if err := tx.Exec(query, args...).Error; err != nil {
		return fmt.Errorf("read model: refresh scrape-source stats: %w", err)
	}
	return nil
}

func RefreshReadModelIndexes(ctx context.Context) error {
	if DB == nil {
		return fmt.Errorf("database not initialised")
	}
	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}
	if err := refreshAllUserProxyFilterIndexes(db); err != nil {
		return err
	}
	return refreshAllUserScrapeSourceStats(db)
}

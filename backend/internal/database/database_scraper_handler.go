package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"magpie/internal/api/dto"
	"magpie/internal/config"
	"magpie/internal/support"

	"github.com/charmbracelet/log"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"magpie/internal/domain"
)

const scrapeSitesPerPage = 20

var ErrNoScrapeSourcesSelected = errors.New("no scraping sources selected for deletion")

// GetScrapingSourcesOfUsers returns all URLs associated with the given user.
func GetScrapingSourcesOfUsers(userID uint) []string {
	var sources []string
	if err := DB.Table("scrape_sites").
		Select("scrape_sites.url").
		Joins("JOIN user_scrape_site uss ON uss.scrape_site_id = scrape_sites.id").
		Where("uss.user_id = ?", userID).
		Order("uss.created_at DESC").
		Scan(&sources).Error; err != nil {
		return nil
	}

	return sources
}

// SaveScrapingSourcesOfUsers appends new sources to the user without removing existing ones.
func SaveScrapingSourcesOfUsers(userID uint, sources []string) ([]domain.ScrapeSite, error) {
	var sites []domain.ScrapeSite
	err := DB.Transaction(func(tx *gorm.DB) error {
		sites = make([]domain.ScrapeSite, 0, len(sources))
		siteIDs := make([]uint64, 0, len(sources))

		// Load the user and existing associations
		var user domain.User
		if err := tx.Preload("ScrapeSites").First(&user, userID).Error; err != nil {
			return err
		}

		existing := make(map[string]struct{}, len(user.ScrapeSites))
		for _, s := range user.ScrapeSites {
			existing[s.URL] = struct{}{}
		}

		seen := make(map[string]struct{}, len(sources))

		// Create or find each ScrapeSite that is not already associated
		for _, raw := range sources {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" || !support.IsValidURL(trimmed) {
				continue
			}
			if config.IsWebsiteBlocked(trimmed) {
				log.Info("Skipped blocked scrape source", "url", trimmed)
				continue
			}
			if _, ok := existing[trimmed]; ok {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}

			var site domain.ScrapeSite
			if err := tx.Where("url = ?", trimmed).
				FirstOrCreate(&site, &domain.ScrapeSite{URL: trimmed}).Error; err != nil {
				return err
			}
			sites = append(sites, site)
			siteIDs = append(siteIDs, site.ID)
		}

		// Append only new associations
		if len(sites) > 0 {
			if err := tx.Model(&user).
				Association("ScrapeSites").
				Append(&sites); err != nil {
				return err
			}

			// Reload newly touched sites with Users preloaded
			var loaded []domain.ScrapeSite
			if err := tx.
				Preload("Users", preloadUserIDsOnly).
				Where("id IN ?", siteIDs).
				Find(&loaded).Error; err != nil {
				return err
			}
			sites = loaded
		}

		return nil
	})

	return sites, err
}

func GetAllScrapeSites() ([]domain.ScrapeSite, error) {
	var allProxies []domain.ScrapeSite
	const batchSize = maxParamsPerBatch

	collectedProxies := make([]domain.ScrapeSite, 0)

	err := DB.
		Model(&domain.ScrapeSite{}).
		Distinct("scrape_sites.*").
		Joins("JOIN user_scrape_site uss ON uss.scrape_site_id = scrape_sites.id").
		Preload("Users", preloadUserIDsOnly).
		Order("scrape_sites.id").
		FindInBatches(&allProxies, batchSize, func(tx *gorm.DB, batch int) error {
			collectedProxies = append(collectedProxies, allProxies...)
			return nil
		})

	if err.Error != nil {
		return nil, err.Error
	}

	return collectedProxies, nil
}

func AssociateProxiesToScrapeSite(siteID uint64, proxies []domain.Proxy) error {
	if len(proxies) == 0 {
		return nil
	}

	// ProxyScrapeSite inserts touch proxy_id, scrape_site_id and created_at columns.
	const paramsPerRow = 3
	chunkSize := maxParamsPerBatch / paramsPerRow
	if chunkSize > len(proxies) {
		chunkSize = len(proxies)
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(proxies); start += chunkSize {
			end := start + chunkSize
			if end > len(proxies) {
				end = len(proxies)
			}

			assoc := make([]domain.ProxyScrapeSite, 0, end-start)
			for _, p := range proxies[start:end] {
				if p.ID == 0 {
					continue
				}
				assoc = append(assoc, domain.ProxyScrapeSite{
					ProxyID:      p.ID,
					ScrapeSiteID: siteID,
				})
			}

			if len(assoc) == 0 {
				continue
			}

			if err := tx.
				Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "proxy_id"}, {Name: "scrape_site_id"}},
					DoNothing: true,
				}).
				Create(&assoc).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

func GetAllScrapeSiteCountOfUser(userId uint) int64 {
	return GetAllScrapeSiteCountOfUserWithSearch(userId, "")
}

func GetAllScrapeSiteCountOfUserWithSearch(userId uint, search string) int64 {
	return GetAllScrapeSiteCountOfUserWithSearchAndFilters(userId, search, dto.ScrapeSourceListFilters{})
}

func GetAllScrapeSiteCountOfUserWithSearchAndFilters(userId uint, search string, filters dto.ScrapeSourceListFilters) int64 {
	var count int64
	query := buildScrapeSiteInfoQuery(userId)
	query = applyScrapeSiteSearch(query, search)
	query = applyScrapeSiteListFilters(query, filters)
	query.Count(&count)
	return count
}

func GetScrapeSiteInfoPage(userId uint, page int) []dto.ScrapeSiteInfo {
	return GetScrapeSiteInfoPageWithSearch(userId, page, "")
}

func GetScrapeSiteInfoPageWithSearch(userId uint, page int, search string) []dto.ScrapeSiteInfo {
	return GetScrapeSiteInfoPageWithSearchAndFilters(userId, page, search, dto.ScrapeSourceListFilters{})
}

func GetScrapeSiteInfoPageWithSearchAndFilters(userId uint, page int, search string, filters dto.ScrapeSourceListFilters) []dto.ScrapeSiteInfo {
	offset := (page - 1) * scrapeSitesPerPage

	var results []dto.ScrapeSiteInfo

	query := buildScrapeSiteInfoQuery(userId).Select(
		"scrape_sites.id         AS id, " +
			"scrape_sites.url        AS url, " +
			"COALESCE(pc.proxy_count, 0) AS proxy_count, " +
			"COALESCE(ps.alive_count, 0) AS alive_count, " +
			"COALESCE(ps.dead_count, 0) AS dead_count, " +
			"COALESCE(ps.unknown_count, 0) AS unknown_count, " +
			"uss.created_at          AS added_at",
	)

	query = applyScrapeSiteSearch(query, search)
	query = applyScrapeSiteListFilters(query, filters)

	query.Order("uss.created_at DESC").
		Offset(offset).
		Limit(scrapeSitesPerPage).
		Scan(&results)

	return results
}

func GetScrapeSiteInfoForExport(userId uint, settings dto.ScrapeSourceExportSettings) ([]dto.ScrapeSiteInfo, error) {
	var results []dto.ScrapeSiteInfo

	query := buildScrapeSiteInfoQuery(userId).Select(
		"scrape_sites.id         AS id, " +
			"scrape_sites.url        AS url, " +
			"COALESCE(pc.proxy_count, 0) AS proxy_count, " +
			"COALESCE(ps.alive_count, 0) AS alive_count, " +
			"COALESCE(ps.dead_count, 0) AS dead_count, " +
			"COALESCE(ps.unknown_count, 0) AS unknown_count, " +
			"uss.created_at          AS added_at",
	)

	if len(settings.ScrapeSources) > 0 {
		query = query.Where("scrape_sites.id IN ?", settings.ScrapeSources)
	}

	if settings.Filter {
		protocols := scrapeSourceProtocolsForExport(settings)
		if len(protocols) > 0 {
			query = query.Where("LOWER(split_part(scrape_sites.url, '://', 1)) IN ?", protocols)
		}
		proxyCount := settings.ProxyCount
		if proxyCount == 0 {
			proxyCount = settings.MinProxyCount
		}
		if proxyCount > 0 {
			if exportCountOperator(settings.ProxyCountOperator, settings.ProxyCountMode) == "<" {
				query = query.Where("COALESCE(pc.proxy_count, 0) < ?", proxyCount)
			} else {
				query = query.Where("COALESCE(pc.proxy_count, 0) > ?", proxyCount)
			}
		}
		aliveCount := settings.AliveCount
		if aliveCount == 0 {
			aliveCount = settings.MinAliveCount
		}
		if aliveCount > 0 {
			if exportCountOperator(settings.AliveCountOperator, "") == "<" {
				query = query.Where("COALESCE(ps.alive_count, 0) < ?", aliveCount)
			} else {
				query = query.Where("COALESCE(ps.alive_count, 0) > ?", aliveCount)
			}
		}
	}

	err := query.Order("uss.created_at DESC").Scan(&results).Error
	return results, err
}

func scrapeSourceProtocolsForExport(settings dto.ScrapeSourceExportSettings) []string {
	if !settings.Filter {
		return nil
	}

	protocols := make([]string, 0, 2)
	if settings.Http {
		protocols = append(protocols, "http")
	}
	if settings.Https {
		protocols = append(protocols, "https")
	}

	return protocols
}

func applyScrapeSiteSearch(query *gorm.DB, search string) *gorm.DB {
	normalized := strings.TrimSpace(search)
	if normalized == "" {
		return query
	}

	return query.Where("scrape_sites.url ILIKE ?", "%"+normalized+"%")
}

func applyScrapeSiteListFilters(query *gorm.DB, filters dto.ScrapeSourceListFilters) *gorm.DB {
	protocols := normalizeScrapeSourceProtocolFilters(filters.Protocols)
	if len(protocols) > 0 {
		query = query.Where("LOWER(split_part(scrape_sites.url, '://', 1)) IN ?", protocols)
	}

	if filters.ProxyCount > 0 {
		if exportCountOperator(filters.ProxyCountOperator, "") == "<" {
			query = query.Where("COALESCE(pc.proxy_count, 0) < ?", filters.ProxyCount)
		} else {
			query = query.Where("COALESCE(pc.proxy_count, 0) > ?", filters.ProxyCount)
		}
	}

	if filters.AliveCount > 0 {
		if exportCountOperator(filters.AliveCountOperator, "") == "<" {
			query = query.Where("COALESCE(ps.alive_count, 0) < ?", filters.AliveCount)
		} else {
			query = query.Where("COALESCE(ps.alive_count, 0) > ?", filters.AliveCount)
		}
	}

	return query
}

func normalizeScrapeSourceProtocolFilters(protocols []string) []string {
	if len(protocols) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(protocols))
	out := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		trimmed := strings.ToLower(strings.TrimSpace(protocol))
		if trimmed != "http" && trimmed != "https" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func exportCountOperator(operator string, legacyMode string) string {
	trimmed := strings.TrimSpace(operator)
	if trimmed == "<" || trimmed == ">" {
		return trimmed
	}
	if strings.EqualFold(legacyMode, "max") {
		return "<"
	}
	return ">"
}

func buildScrapeSiteInfoQuery(userId uint) *gorm.DB {
	// subquery: for each scrape_site_id, count only the proxies that this user has
	subQuery := DB.
		Model(&domain.ProxyScrapeSite{}).
		Select("scrape_site_id, COUNT(*) AS proxy_count").
		Joins("JOIN user_proxies up ON up.proxy_id = proxy_scrape_site.proxy_id AND up.user_id = ?", userId).
		Group("scrape_site_id")

	statusQuery := DB.
		Table("proxy_scrape_site pss").
		Select(
			"pss.scrape_site_id AS scrape_site_id, "+
				"COALESCE(SUM(CASE WHEN pos.overall_alive IS TRUE THEN 1 ELSE 0 END), 0) AS alive_count, "+
				"COALESCE(SUM(CASE WHEN pos.overall_alive IS FALSE THEN 1 ELSE 0 END), 0) AS dead_count, "+
				"COALESCE(SUM(CASE WHEN pos.proxy_id IS NULL THEN 1 ELSE 0 END), 0) AS unknown_count",
		).
		Joins("JOIN user_proxies up ON up.proxy_id = pss.proxy_id AND up.user_id = ?", userId).
		Joins("LEFT JOIN proxy_overall_statuses pos ON pos.proxy_id = pss.proxy_id").
		Group("pss.scrape_site_id")

	return DB.
		Model(&domain.ScrapeSite{}).
		// only the sites this user has added
		Joins("JOIN user_scrape_site uss ON uss.scrape_site_id = scrape_sites.id AND uss.user_id = ?", userId).
		// attach the per-site, per-user proxy counts
		Joins("LEFT JOIN (?) AS pc ON pc.scrape_site_id = scrape_sites.id", subQuery).
		Joins("LEFT JOIN (?) AS ps ON ps.scrape_site_id = scrape_sites.id", statusQuery)
}

type scrapeSiteAggregateRow struct {
	ProxyCount       uint            `gorm:"column:proxy_count"`
	AliveCount       uint            `gorm:"column:alive_count"`
	DeadCount        uint            `gorm:"column:dead_count"`
	UnknownCount     uint            `gorm:"column:unknown_count"`
	AvgReputation    sql.NullFloat64 `gorm:"column:avg_reputation"`
	LastProxyAddedAt sql.NullTime    `gorm:"column:last_proxy_added_at"`
	LastCheckedAt    sql.NullTime    `gorm:"column:last_checked_at"`
}

func GetScrapeSiteDetail(userId uint, scrapeSiteId uint64) (*dto.ScrapeSiteDetail, error) {
	if scrapeSiteId == 0 {
		return nil, nil
	}

	type scrapeSiteBaseRow struct {
		Id      uint64    `gorm:"column:id"`
		Url     string    `gorm:"column:url"`
		AddedAt time.Time `gorm:"column:added_at"`
	}

	var base scrapeSiteBaseRow
	baseResult := DB.Model(&domain.ScrapeSite{}).
		Select(
			"scrape_sites.id AS id, "+
				"scrape_sites.url AS url, "+
				"uss.created_at AS added_at",
		).
		Joins("JOIN user_scrape_site uss ON uss.scrape_site_id = scrape_sites.id AND uss.user_id = ?", userId).
		Where("scrape_sites.id = ?", scrapeSiteId).
		Limit(1).
		Scan(&base)
	if baseResult.Error != nil {
		return nil, baseResult.Error
	}
	if baseResult.RowsAffected == 0 {
		return nil, nil
	}

	var stats scrapeSiteAggregateRow
	statsResult := DB.Table("proxy_scrape_site pss").
		Select(
			"COUNT(DISTINCT pss.proxy_id) AS proxy_count, "+
				"COALESCE(SUM(CASE WHEN pos.overall_alive IS TRUE THEN 1 ELSE 0 END), 0) AS alive_count, "+
				"COALESCE(SUM(CASE WHEN pos.overall_alive IS FALSE THEN 1 ELSE 0 END), 0) AS dead_count, "+
				"COALESCE(SUM(CASE WHEN pos.proxy_id IS NULL THEN 1 ELSE 0 END), 0) AS unknown_count, "+
				"AVG(pr.score) AS avg_reputation, "+
				"MAX(pss.created_at) AS last_proxy_added_at, "+
				"MAX(pos.last_checked_at) AS last_checked_at",
		).
		Joins("JOIN user_proxies up ON up.proxy_id = pss.proxy_id AND up.user_id = ?", userId).
		Joins("LEFT JOIN proxy_overall_statuses pos ON pos.proxy_id = pss.proxy_id").
		Joins("LEFT JOIN proxy_reputations pr ON pr.proxy_id = pss.proxy_id AND pr.kind = ?", domain.ProxyReputationKindOverall).
		Where("pss.scrape_site_id = ?", scrapeSiteId).
		Scan(&stats)
	if statsResult.Error != nil {
		return nil, statsResult.Error
	}

	type reputationCount struct {
		Label string `gorm:"column:label"`
		Count uint   `gorm:"column:count"`
	}

	var repCounts []reputationCount
	repResult := DB.Table("proxy_scrape_site pss").
		Select("LOWER(COALESCE(NULLIF(pr.label, ''), 'unknown')) AS label, COUNT(*) AS count").
		Joins("JOIN user_proxies up ON up.proxy_id = pss.proxy_id AND up.user_id = ?", userId).
		Joins("LEFT JOIN proxy_reputations pr ON pr.proxy_id = pss.proxy_id AND pr.kind = ?", domain.ProxyReputationKindOverall).
		Where("pss.scrape_site_id = ?", scrapeSiteId).
		Group("label").
		Scan(&repCounts)
	if repResult.Error != nil {
		return nil, repResult.Error
	}

	breakdown := dto.ScrapeSiteReputationBreakdown{}
	for _, row := range repCounts {
		switch row.Label {
		case "good":
			breakdown.Good = row.Count
		case "neutral":
			breakdown.Neutral = row.Count
		case "poor":
			breakdown.Poor = row.Count
		default:
			breakdown.Unknown += row.Count
		}
	}

	var avgReputation *float32
	if stats.AvgReputation.Valid {
		avgReputation = new(float32(stats.AvgReputation.Float64))
	}

	var lastProxyAddedAt *time.Time
	if stats.LastProxyAddedAt.Valid {
		lastProxyAddedAt = new(stats.LastProxyAddedAt.Time)
	}

	var lastCheckedAt *time.Time
	if stats.LastCheckedAt.Valid {
		lastCheckedAt = new(stats.LastCheckedAt.Time)
	}

	detail := &dto.ScrapeSiteDetail{
		Id:                  base.Id,
		Url:                 base.Url,
		AddedAt:             base.AddedAt,
		ProxyCount:          stats.ProxyCount,
		AliveCount:          stats.AliveCount,
		DeadCount:           stats.DeadCount,
		UnknownCount:        stats.UnknownCount,
		AvgReputation:       avgReputation,
		LastProxyAddedAt:    lastProxyAddedAt,
		LastCheckedAt:       lastCheckedAt,
		ReputationBreakdown: breakdown,
	}

	return detail, nil
}

func GetScrapeSiteProxyPage(userId uint, scrapeSiteId uint64, page int, pageSize int, search string, filters dto.ProxyListFilters) ([]dto.ProxyInfo, int64, error) {
	return GetScrapeSiteProxyPageWithOptions(userId, scrapeSiteId, page, pageSize, search, filters, ProxyPageQueryOptions{
		IncludeHealth:     true,
		IncludeReputation: true,
	})
}

func GetScrapeSiteProxyPageWithOptions(userId uint, scrapeSiteId uint64, page int, pageSize int, search string, filters dto.ProxyListFilters, options ProxyPageQueryOptions) ([]dto.ProxyInfo, int64, error) {
	if scrapeSiteId == 0 {
		return []dto.ProxyInfo{}, 0, nil
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > maxProxiesPerPage {
		pageSize = proxiesPerPage
	}

	options = normalizeProxyPageQueryOptions(options)

	subQuery := buildLatestProxyStatisticSubQuery()

	healthSelect := "NULL::numeric AS health_overall, " +
		"NULL::numeric AS health_http, " +
		"NULL::numeric AS health_https, " +
		"NULL::numeric AS health_socks4, " +
		"NULL::numeric AS health_socks5"
	if options.IncludeHealth {
		healthSelect = "stats.health_overall AS health_overall, " +
			"stats.health_http AS health_http, " +
			"stats.health_https AS health_https, " +
			"stats.health_socks4 AS health_socks4, " +
			"stats.health_socks5 AS health_socks5"
	}

	query := DB.Model(&domain.Proxy{}).
		Select(
			"proxies.id AS id, "+
				"proxies.ip AS ip_encrypted, "+
				"proxies.port AS port, "+
				"COALESCE(NULLIF(proxies.estimated_type, ''), 'N/A') AS estimated_type, "+
				"COALESCE(ps.response_time, 0) AS response_time, "+
				"COALESCE(NULLIF(proxies.country, ''), 'N/A') AS country, "+
				"COALESCE(al.name, 'N/A') AS anonymity_level, "+
				"COALESCE(pos.overall_alive, false) AS alive, "+
				healthSelect+", "+
				"COALESCE(pos.last_checked_at, ps.created_at, '0001-01-01 00:00:00'::timestamp) AS latest_check",
		).
		Joins("JOIN user_proxies up ON up.proxy_id = proxies.id AND up.user_id = ?", userId).
		Joins("JOIN proxy_scrape_site pss ON pss.proxy_id = proxies.id AND pss.scrape_site_id = ?", scrapeSiteId).
		Joins("JOIN user_scrape_site uss ON uss.scrape_site_id = pss.scrape_site_id AND uss.user_id = ?", userId).
		Joins("LEFT JOIN (?) AS ps ON ps.proxy_id = proxies.id", subQuery).
		Joins("LEFT JOIN proxy_overall_statuses pos ON pos.proxy_id = proxies.id").
		Joins("LEFT JOIN anonymity_levels al ON al.id = ps.level_id")

	if options.IncludeHealth {
		healthSubQuery := buildProxyHealthSubQuery(userId)
		query = query.Joins("LEFT JOIN (?) AS stats ON stats.proxy_id = proxies.id", healthSubQuery)
	}
	if options.SortField == "reputation" {
		query = query.Joins("LEFT JOIN proxy_reputations pr_overall ON pr_overall.proxy_id = proxies.id AND pr_overall.kind = ?", domain.ProxyReputationKindOverall)
	}

	query = applyProxyPageSort(query, options)

	filterQuery := buildProxyListFilterQuery(userId, filters)
	if filterQuery != nil {
		query = query.Where("proxies.id IN (?)", filterQuery)
	}

	rows := make([]dto.ProxyInfoRow, 0)
	normalizedSearch := strings.TrimSpace(search)

	if normalizedSearch == "" {
		offset := (page - 1) * pageSize
		query = query.Offset(offset).Limit(pageSize)
		if err := query.Scan(&rows).Error; err != nil {
			return []dto.ProxyInfo{}, 0, err
		}

		proxies := proxyInfoRowsToDTO(rows)
		if options.IncludeReputation {
			attachReputationsToProxyInfos(proxies)
		}

		var total int64
		countQuery := DB.Model(&domain.Proxy{}).
			Joins("JOIN user_proxies up ON up.proxy_id = proxies.id AND up.user_id = ?", userId).
			Joins("JOIN proxy_scrape_site pss ON pss.proxy_id = proxies.id AND pss.scrape_site_id = ?", scrapeSiteId).
			Joins("JOIN user_scrape_site uss ON uss.scrape_site_id = pss.scrape_site_id AND uss.user_id = ?", userId)
		if filterQuery != nil {
			countQuery = countQuery.Where("proxies.id IN (?)", filterQuery)
		}
		if err := countQuery.Distinct("proxies.id").Count(&total).Error; err != nil {
			return proxies, 0, err
		}

		return proxies, total, nil
	}

	if err := query.Scan(&rows).Error; err != nil {
		return []dto.ProxyInfo{}, 0, err
	}

	proxies := proxyInfoRowsToDTO(rows)
	if options.IncludeReputation {
		attachReputationsToProxyInfos(proxies)
	}
	filtered := filterProxiesBySearch(proxies, normalizedSearch)
	total := int64(len(filtered))
	start := (page - 1) * pageSize
	if start >= len(filtered) {
		return []dto.ProxyInfo{}, total, nil
	}

	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}

	pageSlice := filtered[start:end]
	if options.IncludeReputation {
		attachReputationsToProxyInfos(pageSlice)
	}

	return pageSlice, total, nil
}

func DeleteOrphanProxyScrapeSiteRelations(ctx context.Context) (int64, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialised")
	}

	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}

	if !isPostgresDialect(db) {
		result := db.
			Where("NOT EXISTS (SELECT 1 FROM proxies p WHERE p.id = proxy_scrape_site.proxy_id)").
			Delete(&domain.ProxyScrapeSite{})
		return result.RowsAffected, result.Error
	}

	const orphanProxyBatchSize = 5_000
	var total int64
	for {
		result := db.Exec(`
			WITH orphan_proxy_ids AS (
				SELECT pss.proxy_id
				FROM proxy_scrape_site AS pss
				LEFT JOIN proxies AS p ON p.id = pss.proxy_id
				WHERE p.id IS NULL
				GROUP BY pss.proxy_id
				ORDER BY pss.proxy_id
				LIMIT ?
			)
			DELETE FROM proxy_scrape_site AS pss
			USING orphan_proxy_ids AS orphan
			WHERE pss.proxy_id = orphan.proxy_id
		`, orphanProxyBatchSize)
		if result.Error != nil {
			return total, result.Error
		}

		total += result.RowsAffected
		if result.RowsAffected == 0 {
			return total, nil
		}
	}
}

func DeleteScrapeSiteRelation(userId uint, scrapeSite []int) (int64, []domain.ScrapeSite, error) {
	if len(scrapeSite) == 0 {
		return 0, nil, nil
	}

	chunkSize := deleteChunkSize
	if chunkSize > len(scrapeSite) {
		chunkSize = len(scrapeSite)
	}
	if chunkSize <= 0 {
		chunkSize = len(scrapeSite)
	}

	var totalDeleted int64
	orphanSet := make(map[uint64]struct{})

	for start := 0; start < len(scrapeSite); start += chunkSize {
		end := start + chunkSize
		if end > len(scrapeSite) {
			end = len(scrapeSite)
		}

		chunk := scrapeSite[start:end]
		result := DB.
			Where("scrape_site_id IN ?", chunk).
			Where("user_id = ?", userId).
			Delete(&domain.UserScrapeSite{})

		if result.Error != nil {
			return totalDeleted, nil, result.Error
		}

		totalDeleted += result.RowsAffected

		orphanIDs, err := collectOrphanScrapeSiteIDs(chunk)
		if err != nil {
			return totalDeleted, nil, err
		}

		for _, id := range orphanIDs {
			orphanSet[id] = struct{}{}
		}
	}

	if len(orphanSet) == 0 {
		return totalDeleted, nil, nil
	}

	uniqueIDs := make([]uint64, 0, len(orphanSet))
	for id := range orphanSet {
		uniqueIDs = append(uniqueIDs, id)
	}

	var orphans []domain.ScrapeSite
	if err := DB.Where("id IN ?", uniqueIDs).Find(&orphans).Error; err != nil {
		return totalDeleted, nil, err
	}

	return totalDeleted, orphans, nil
}

func DeleteScrapeSitesWithSettings(userID uint, settings dto.ScrapeSourceDeleteSettings) (int64, []domain.ScrapeSite, error) {
	if settings.Scope == "selected" && len(settings.ScrapeSources) == 0 {
		return 0, nil, ErrNoScrapeSourcesSelected
	}

	exportSettings := dto.ScrapeSourceExportSettings{
		ScrapeSources:      settings.ScrapeSources,
		Filter:             settings.Filter,
		Http:               settings.Http,
		Https:              settings.Https,
		ProxyCountOperator: settings.ProxyCountOperator,
		ProxyCount:         settings.ProxyCount,
		AliveCountOperator: settings.AliveCountOperator,
		AliveCount:         settings.AliveCount,
	}

	sources, err := GetScrapeSiteInfoForExport(userID, exportSettings)
	if err != nil {
		return 0, nil, err
	}

	if len(sources) == 0 {
		return 0, nil, nil
	}

	sourceIDs := make([]int, 0, len(sources))
	for _, source := range sources {
		sourceIDs = append(sourceIDs, int(source.Id))
	}

	return DeleteScrapeSiteRelation(userID, sourceIDs)
}

func collectOrphanScrapeSiteIDs(candidateIDs []int) ([]uint64, error) {
	if len(candidateIDs) == 0 {
		return nil, nil
	}

	var stillInUse []int
	if err := DB.Model(&domain.UserScrapeSite{}).
		Where("scrape_site_id IN ?", candidateIDs).
		Distinct("scrape_site_id").
		Pluck("scrape_site_id", &stillInUse).Error; err != nil {
		return nil, err
	}

	inUseSet := make(map[int]struct{}, len(stillInUse))
	for _, id := range stillInUse {
		inUseSet[id] = struct{}{}
	}

	seen := make(map[int]struct{}, len(candidateIDs))
	orphanIDs := make([]uint64, 0, len(candidateIDs))
	for _, candidate := range candidateIDs {
		if _, alreadySeen := seen[candidate]; alreadySeen {
			continue
		}
		seen[candidate] = struct{}{}

		if _, ok := inUseSet[candidate]; ok {
			continue
		}

		orphanIDs = append(orphanIDs, uint64(candidate))
	}

	if len(orphanIDs) == 0 {
		return nil, nil
	}

	return orphanIDs, nil
}

func DeleteOrphanScrapeSites(ctx context.Context) (int64, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialised")
	}

	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}

	result := db.
		Where("NOT EXISTS (SELECT 1 FROM user_scrape_site us WHERE us.scrape_site_id = scrape_sites.id)").
		Delete(&domain.ScrapeSite{})
	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

func ScrapeSiteHasUsers(siteID uint64) (bool, error) {
	var count int64
	if err := DB.Model(&domain.UserScrapeSite{}).
		Where("scrape_site_id = ?", siteID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

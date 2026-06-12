package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"magpie/internal/api/dto"
	"magpie/internal/config"
	"magpie/internal/domain"
	"magpie/internal/security"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	batchThreshold             = 8191  // Use batches when exceeding this number of records
	maxParamsPerBatch          = 65534 // Conservative default (PostgreSQL's limit) - 1
	minBatchSize               = 100   // Minimum batch size to maintain efficiency
	deleteChunkSize            = 5000  // Keep large deletes under Postgres parameter limits
	autoRemoveCleanupBatchSize = 2000
	proxyIterationBatchSize    = 2000
	proxyExportBatchSize       = 2000

	proxiesPerPage    = 40
	maxProxiesPerPage = 100

	defaultRecentProxyChecksLimit = 8
	maxRecentProxyChecksLimit     = 50
	dashboardFastestAliveLimit    = 100
)

var ErrNoProxiesSelected = errors.New("no proxies selected for deletion")

type dashboardProxyListCacheKey struct {
	UserID uint
	Limit  int
}

var dashboardRecentChecksCache sync.Map
var dashboardFastestAliveCache sync.Map

type ProxyPageQueryOptions struct {
	IncludeHealth     bool
	IncludeReputation bool
	SortField         string
	SortOrder         string
}

func InsertAndGetProxiesWithUser(proxies []domain.Proxy, userIDs ...uint) ([]domain.Proxy, error) {
	inserted, err := insertAndAssociateProxies(proxies, userIDs)
	if err != nil || len(inserted) == 0 {
		return inserted, err
	}

	proxiesWithUsers, err := fetchProxiesWithUsers(DB, inserted)
	if err != nil {
		return nil, err
	}

	return proxiesWithUsers, nil
}

func insertAndAssociateProxies(proxies []domain.Proxy, userIDs []uint) ([]domain.Proxy, error) {
	if len(proxies) == 0 || len(userIDs) == 0 {
		return nil, nil
	}

	userIDs = normalizeUserIDs(userIDs)
	if len(userIDs) == 0 {
		return nil, nil
	}

	uniqueProxies := deduplicateProxies(proxies)
	if len(uniqueProxies) == 0 {
		return nil, nil
	}

	batchSize := calculateBatchSize(len(uniqueProxies))
	limitCfg := config.GetConfig().ProxyLimits

	tx := DB.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer transactionRollbackHandler(tx)

	perUserHashes := make(map[uint][]string, len(userIDs))
	allowedHashes := make(map[string]struct{})

	for _, userID := range userIDs {
		if err := lockUserForProxyLimit(tx, userID, limitCfg); err != nil {
			tx.Rollback()
			return nil, err
		}

		hashes, err := filterHashesForUser(tx, uniqueProxies, userID, batchSize, limitCfg)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if len(hashes) == 0 {
			continue
		}
		perUserHashes[userID] = hashes
		for _, hash := range hashes {
			allowedHashes[hash] = struct{}{}
		}
	}

	if len(allowedHashes) == 0 {
		tx.Rollback()
		return nil, nil
	}

	uniqueProxies = filterProxiesByHash(uniqueProxies, allowedHashes)

	if err := insertProxies(tx, uniqueProxies, batchSize); err != nil {
		tx.Rollback()
		return nil, err
	}

	if err := ensureProxyIDs(tx, uniqueProxies); err != nil {
		tx.Rollback()
		return nil, err
	}

	hashToID := make(map[string]uint64, len(uniqueProxies))
	for i := range uniqueProxies {
		hashToID[string(uniqueProxies[i].Hash)] = uniqueProxies[i].ID
	}

	for _, userID := range userIDs {
		hashes := perUserHashes[userID]
		if len(hashes) == 0 {
			continue
		}

		proxyIDs := make([]uint64, 0, len(hashes))
		for _, hash := range hashes {
			if id, ok := hashToID[hash]; ok {
				proxyIDs = append(proxyIDs, id)
			}
		}
		if len(proxyIDs) == 0 {
			continue
		}

		if err := createUserAssociations(tx, proxyIDs, userID, batchSize); err != nil {
			tx.Rollback()
			return nil, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	return uniqueProxies, nil
}

// Helper functions
func normalizeUserIDs(userIDs []uint) []uint {
	if len(userIDs) == 0 {
		return nil
	}

	normalized := append([]uint(nil), userIDs...)
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i] < normalized[j]
	})

	out := normalized[:0]
	var previous uint
	hasPrevious := false
	for _, userID := range normalized {
		if hasPrevious && userID == previous {
			continue
		}
		out = append(out, userID)
		previous = userID
		hasPrevious = true
	}

	return out
}

func lockUserForProxyLimit(tx *gorm.DB, userID uint, limitCfg config.ProxyLimitConfig) error {
	if tx == nil || !limitCfg.Enabled {
		return nil
	}
	// PostgreSQL needs explicit row locks to serialize count-and-insert across
	// concurrent transactions for the same user. Other dialects are left as-is.
	if tx.Dialector.Name() != "postgres" {
		return nil
	}

	var userIDRow uint
	if err := tx.Model(&domain.User{}).
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", userID).
		Take(&userIDRow).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("proxy limit lock: user %d not found", userID)
		}
		return err
	}

	return nil
}

func deduplicateProxies(proxies []domain.Proxy) []domain.Proxy {
	seen := make(map[string]struct{}, len(proxies))
	unique := make([]domain.Proxy, 0, len(proxies))
	for _, p := range proxies {
		p.GenerateHash()
		key := string(p.Hash)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			unique = append(unique, p)
		}
	}
	return unique
}

func calculateBatchSize(proxyCount int) int {
	if proxyCount <= batchThreshold {
		return proxyCount
	}

	numFields, err := getNumDatabaseFields(domain.Proxy{}, DB)
	if err != nil || numFields == 0 {
		return minBatchSize // Fallback to safe minimum
	}

	batchSize := maxParamsPerBatch / numFields
	return clamp(batchSize, minBatchSize, proxyCount)
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func insertProxies(tx *gorm.DB, proxies []domain.Proxy, batchSize int) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "hash"}},
		DoUpdates: clause.AssignmentColumns([]string{"hash", "ip_hash", "ip_int"}), // To get the ids from duplicates
	}).CreateInBatches(proxies, batchSize).Error
}

func filterHashesForUser(tx *gorm.DB, proxies []domain.Proxy, userID uint, chunkSize int, limitCfg config.ProxyLimitConfig) ([]string, error) {
	if len(proxies) == 0 {
		return nil, nil
	}

	if !limitCfg.Enabled {
		return collectHashes(proxies), nil
	}

	if limitCfg.ExcludeAdmins {
		var role string
		if err := tx.Model(&domain.User{}).
			Select("role").
			Where("id = ?", userID).
			Scan(&role).Error; err != nil {
			return nil, err
		}
		if role == "admin" {
			return collectHashes(proxies), nil
		}
	}

	existingSet, err := getExistingHashesForUser(tx, userID, proxies, chunkSize)
	if err != nil {
		return nil, err
	}

	var currentCount int64
	if err := tx.Table("user_proxies").
		Where("user_id = ?", userID).
		Count(&currentCount).Error; err != nil {
		return nil, err
	}

	available := int64(limitCfg.MaxPerUser) - currentCount
	if available < 0 {
		available = 0
	}

	allowed := make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		key := string(proxy.Hash)
		if _, ok := existingSet[key]; ok {
			allowed = append(allowed, key)
			continue
		}
		if available == 0 {
			continue
		}
		allowed = append(allowed, key)
		available--
	}

	return allowed, nil
}

func collectHashes(proxies []domain.Proxy) []string {
	if len(proxies) == 0 {
		return nil
	}

	hashes := make([]string, len(proxies))
	for i, proxy := range proxies {
		hashes[i] = string(proxy.Hash)
	}
	return hashes
}

func getExistingHashesForUser(tx *gorm.DB, userID uint, proxies []domain.Proxy, chunkSize int) (map[string]struct{}, error) {
	existing := make(map[string]struct{}, len(proxies))
	if len(proxies) == 0 {
		return existing, nil
	}

	if chunkSize <= 0 || chunkSize > maxParamsPerBatch {
		chunkSize = maxParamsPerBatch
		if len(proxies) < chunkSize {
			chunkSize = len(proxies)
		}
		if chunkSize == 0 {
			chunkSize = minBatchSize
		}
	}

	hashes := make([][]byte, len(proxies))
	for i, proxy := range proxies {
		hashes[i] = proxy.Hash
	}

	for i := 0; i < len(hashes); i += chunkSize {
		end := i + chunkSize
		if end > len(hashes) {
			end = len(hashes)
		}

		var rows [][]byte
		err := tx.Table("user_proxies up").
			Joins("JOIN proxies p ON up.proxy_id = p.id").
			Where("up.user_id = ? AND p.hash IN ?", userID, hashes[i:end]).
			Pluck("p.hash", &rows).Error
		if err != nil {
			return nil, err
		}

		for _, hash := range rows {
			existing[string(hash)] = struct{}{}
		}
	}

	return existing, nil
}

func filterProxiesByHash(proxies []domain.Proxy, allowed map[string]struct{}) []domain.Proxy {
	if len(allowed) == 0 {
		return nil
	}

	filtered := proxies[:0]
	for _, proxy := range proxies {
		if _, ok := allowed[string(proxy.Hash)]; ok {
			filtered = append(filtered, proxy)
		}
	}
	return filtered
}

func ensureProxyIDs(tx *gorm.DB, proxies []domain.Proxy) error {
	var missing [][]byte
	for _, proxy := range proxies {
		if proxy.ID == 0 {
			missing = append(missing, proxy.Hash)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	var results []struct {
		ID   uint64
		Hash []byte
	}
	if err := tx.Model(&domain.Proxy{}).
		Select("id, hash").
		Where("hash IN ?", missing).
		Find(&results).Error; err != nil {
		return err
	}

	lookup := make(map[string]uint64, len(results))
	for _, r := range results {
		lookup[string(r.Hash)] = r.ID
	}

	for i, proxy := range proxies {
		if proxy.ID != 0 {
			continue
		}
		if id, ok := lookup[string(proxy.Hash)]; ok {
			proxies[i].ID = id
		}
	}

	return nil
}

func createUserAssociations(tx *gorm.DB, proxyIDs []uint64, userID uint, batchSize int) error {
	if len(proxyIDs) == 0 {
		return nil
	}

	associations := make([]domain.UserProxy, len(proxyIDs))
	for i, id := range proxyIDs {
		associations[i] = domain.UserProxy{
			UserID:  userID,
			ProxyID: id,
		}
	}

	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "proxy_id"}},
		DoNothing: true,
	}).CreateInBatches(associations, batchSize).Error; err != nil {
		return err
	}
	if err := refreshUserProxyFilterIndexesForUserProxyIDs(tx, userID, proxyIDs); err != nil {
		return err
	}
	return refreshUserScrapeSourceStatsForUserProxyIDs(tx, userID, proxyIDs)
}

func fetchProxiesWithUsers(tx *gorm.DB, proxies []domain.Proxy) ([]domain.Proxy, error) {
	ids := make([]uint64, len(proxies))
	for i, p := range proxies {
		ids[i] = p.ID
	}

	var results []domain.Proxy
	for i := 0; i < len(ids); i += maxParamsPerBatch {
		end := i + maxParamsPerBatch
		if end > len(ids) {
			end = len(ids)
		}

		var batch []domain.Proxy
		err := tx.
			Preload("Users", preloadCheckerUsers).
			Where("id IN ?", ids[i:end]).
			Find(&batch).Error
		if err != nil {
			return nil, err
		}
		results = append(results, batch...)
	}
	return results, nil
}

func transactionRollbackHandler(tx *gorm.DB) {
	if r := recover(); r != nil {
		tx.Rollback()
		log.Errorf("Transaction rolled back due to panic: %v", r)
	}
}

func getNumDatabaseFields(model interface{}, db *gorm.DB) (int, error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err != nil {
		return 0, err
	}
	return len(stmt.Schema.DBNames), nil
}

func GetAllProxyCountOfUser(userId uint) int64 {
	var count int64
	DB.Model(&domain.Proxy{}).
		Joins("JOIN user_proxies up ON up.proxy_id = proxies.id AND up.user_id = ?", userId).
		Count(&count)
	return count
}

func ForEachProxyBatch(batchSize int, fn func([]domain.Proxy) error) error {
	if fn == nil {
		return errors.New("for each proxy batch callback is nil")
	}
	if batchSize <= 0 {
		batchSize = proxyIterationBatchSize
	}

	var batchProxies []domain.Proxy
	result := DB.
		Model(&domain.Proxy{}).
		Distinct("proxies.*").
		Joins("JOIN user_proxies up ON up.proxy_id = proxies.id").
		Preload("Users", preloadCheckerUsers).
		Order("proxies.id").
		FindInBatches(&batchProxies, batchSize, func(tx *gorm.DB, _ int) error {
			if len(batchProxies) == 0 {
				return nil
			}

			currentBatch := make([]domain.Proxy, len(batchProxies))
			copy(currentBatch, batchProxies)
			return fn(currentBatch)
		})

	return result.Error
}

func GetProxyInfoPage(userId uint, page int) []dto.ProxyInfo {
	proxies, _ := GetProxyInfoPageWithFilters(userId, page, proxiesPerPage, "", dto.ProxyListFilters{})
	return proxies
}

func GetRecentProxyChecks(userID uint, limit int) []dto.ProxyRecentCheck {
	if limit <= 0 {
		limit = defaultRecentProxyChecksLimit
	} else if limit > maxRecentProxyChecksLimit {
		limit = maxRecentProxyChecksLimit
	}

	key := dashboardProxyListCacheKey{UserID: userID, Limit: limit}
	if cached, ok := dashboardRecentChecksCache.Load(key); ok {
		return cached.([]dto.ProxyRecentCheck)
	}

	return RefreshRecentProxyChecksCache(userID, limit)
}

func RefreshRecentProxyChecksCache(userID uint, limit int) []dto.ProxyRecentCheck {
	if limit <= 0 {
		limit = defaultRecentProxyChecksLimit
	} else if limit > maxRecentProxyChecksLimit {
		limit = maxRecentProxyChecksLimit
	}

	type recentProxyCheckRow struct {
		ID           uint64       `gorm:"column:id"`
		IPEncrypted  string       `gorm:"column:ip_encrypted"`
		Port         uint16       `gorm:"column:port"`
		ResponseTime uint16       `gorm:"column:response_time"`
		Alive        bool         `gorm:"column:alive"`
		LatestCheck  sql.NullTime `gorm:"column:latest_check"`
	}

	rows := make([]recentProxyCheckRow, 0, limit)
	const query = `
WITH candidates AS (
	SELECT
		p.id,
		p.ip AS ip_encrypted,
		p.port,
		COALESCE(pos.overall_alive, FALSE) AS alive,
		pos.last_checked_at
	FROM user_proxies up
	JOIN proxies p ON p.id = up.proxy_id
	LEFT JOIN proxy_overall_statuses pos ON pos.proxy_id = p.id
	WHERE up.user_id = ?
	ORDER BY
		COALESCE(pos.overall_alive, FALSE) DESC,
		pos.last_checked_at DESC NULLS LAST,
		p.id ASC
	LIMIT ?
)
SELECT
	c.id,
	c.ip_encrypted,
	c.port,
	COALESCE(latest.response_time, 0) AS response_time,
	c.alive,
	COALESCE(c.last_checked_at, latest.checked_at) AS latest_check
FROM candidates c
LEFT JOIN LATERAL (
	SELECT pls.response_time, pls.checked_at
	FROM proxy_latest_statistics pls
	WHERE pls.proxy_id = c.id
	ORDER BY pls.checked_at DESC, pls.statistic_id DESC
	LIMIT 1
) latest ON TRUE
ORDER BY c.alive DESC, latest_check DESC, c.id ASC
`
	if err := DB.Raw(query, userID, limit).Scan(&rows).Error; err != nil {
		return nil
	}

	result := make([]dto.ProxyRecentCheck, 0, len(rows))
	for _, row := range rows {
		ip, _, err := security.DecryptProxySecret(row.IPEncrypted)
		if err != nil {
			log.Errorf("decrypt proxy ip: %v", err)
			ip = ""
		}

		latestCheck := time.Time{}
		if row.LatestCheck.Valid {
			latestCheck = row.LatestCheck.Time
		}

		result = append(result, dto.ProxyRecentCheck{
			ID:           row.ID,
			IP:           ip,
			Port:         row.Port,
			ResponseTime: row.ResponseTime,
			Alive:        row.Alive,
			LatestCheck:  latestCheck,
		})
	}

	dashboardRecentChecksCache.Store(
		dashboardProxyListCacheKey{UserID: userID, Limit: limit},
		result,
	)
	return result
}

func GetFastestAliveProxies(userID uint, limit int) []dto.ProxyFastestAlive {
	if limit <= 0 {
		return []dto.ProxyFastestAlive{}
	}

	key := dashboardProxyListCacheKey{UserID: userID, Limit: limit}
	if cached, ok := dashboardFastestAliveCache.Load(key); ok {
		return cached.([]dto.ProxyFastestAlive)
	}

	return RefreshFastestAliveProxiesCache(userID, limit)
}

func RefreshFastestAliveProxiesCache(userID uint, limit int) []dto.ProxyFastestAlive {
	if limit <= 0 {
		return []dto.ProxyFastestAlive{}
	}

	latestAliveStats := DB.Table("proxy_latest_statistics pls").
		Select("DISTINCT ON (pls.proxy_id) pls.proxy_id, pls.response_time, pls.checked_at").
		Where("pls.alive = ?", true).
		Order("pls.proxy_id, pls.response_time ASC, pls.checked_at DESC, pls.statistic_id DESC")

	type fastestAliveProxyRow struct {
		ID              uint64       `gorm:"column:id"`
		IPEncrypted     string       `gorm:"column:ip_encrypted"`
		Port            uint16       `gorm:"column:port"`
		ResponseTime    uint16       `gorm:"column:response_time"`
		Country         string       `gorm:"column:country"`
		ReputationLabel string       `gorm:"column:reputation_label"`
		ReputationScore float32      `gorm:"column:reputation_score"`
		LatestCheck     sql.NullTime `gorm:"column:latest_check"`
	}

	rows := make([]fastestAliveProxyRow, 0, limit)
	if err := DB.Model(&domain.Proxy{}).
		Select(
			"proxies.id AS id, "+
				"proxies.ip AS ip_encrypted, "+
				"proxies.port AS port, "+
				"COALESCE(latest.response_time, 0) AS response_time, "+
				"COALESCE(NULLIF(proxies.country, ''), 'N/A') AS country, "+
				"LOWER(COALESCE(NULLIF(pr.label, ''), 'unknown')) AS reputation_label, "+
				"COALESCE(pr.score, 0) AS reputation_score, "+
				"latest.checked_at AS latest_check",
		).
		Joins("JOIN user_proxies up ON up.proxy_id = proxies.id AND up.user_id = ?", userID).
		Joins("JOIN (?) AS latest ON latest.proxy_id = proxies.id", latestAliveStats).
		Joins("LEFT JOIN proxy_reputations pr ON pr.proxy_id = proxies.id AND pr.kind = ?", domain.ProxyReputationKindOverall).
		Order("latest.response_time ASC, latest.checked_at DESC, proxies.id ASC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil
	}

	result := make([]dto.ProxyFastestAlive, 0, len(rows))
	for _, row := range rows {
		ip, _, err := security.DecryptProxySecret(row.IPEncrypted)
		if err != nil {
			log.Errorf("decrypt fastest alive proxy ip: %v", err)
			ip = ""
		}

		latestCheck := time.Time{}
		if row.LatestCheck.Valid {
			latestCheck = row.LatestCheck.Time
		}

		result = append(result, dto.ProxyFastestAlive{
			ID:              row.ID,
			IP:              ip,
			Port:            row.Port,
			ResponseTime:    row.ResponseTime,
			Country:         row.Country,
			ReputationLabel: row.ReputationLabel,
			ReputationScore: row.ReputationScore,
			LatestCheck:     latestCheck,
		})
	}

	dashboardFastestAliveCache.Store(
		dashboardProxyListCacheKey{UserID: userID, Limit: limit},
		result,
	)
	return result
}

func buildProxyHealthSubQuery(userId uint) *gorm.DB {
	return DB.Table("proxy_latest_statistics pls").
		Select(
			"pls.proxy_id AS proxy_id, "+
				"ROUND(100.0 * AVG(CASE WHEN pls.alive THEN 1 ELSE 0 END)::numeric, 1) AS health_overall, "+
				"MAX(CASE WHEN LOWER(proto.name) = 'http' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_http, "+
				"MAX(CASE WHEN LOWER(proto.name) = 'https' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_https, "+
				"MAX(CASE WHEN LOWER(proto.name) = 'socks4' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_socks4, "+
				"MAX(CASE WHEN LOWER(proto.name) = 'socks5' THEN CASE WHEN pls.alive THEN 100.0 ELSE 0.0 END END) AS health_socks5",
		).
		Joins("JOIN protocols proto ON proto.id = pls.protocol_id").
		Where("EXISTS (SELECT 1 FROM user_proxies up WHERE up.proxy_id = pls.proxy_id AND up.user_id = ?)", userId).
		Group("pls.proxy_id")
}

func buildLatestProxyStatisticSubQuery() *gorm.DB {
	return DB.Table("proxy_latest_statistics pls").
		Select(
			"DISTINCT ON (pls.proxy_id) pls.proxy_id, pls.level_id, " +
				"COALESCE(pls.response_time, 0) AS response_time, " +
				"COALESCE(pls.attempt, 0) AS attempt, pls.checked_at AS created_at",
		).
		Order("pls.proxy_id, pls.checked_at DESC, pls.statistic_id DESC")
}

func GetProxyInfoPageWithFilters(userId uint, page int, pageSize int, search string, filters dto.ProxyListFilters) ([]dto.ProxyInfo, int64) {
	return GetProxyInfoPageWithFiltersAndOptions(userId, page, pageSize, search, filters, ProxyPageQueryOptions{
		IncludeHealth:     true,
		IncludeReputation: true,
	})
}

func GetProxyInfoPageWithFiltersAndOptions(
	userId uint,
	page int,
	pageSize int,
	search string,
	filters dto.ProxyListFilters,
	options ProxyPageQueryOptions,
) ([]dto.ProxyInfo, int64) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > maxProxiesPerPage {
		pageSize = proxiesPerPage
	}

	options = normalizeProxyPageQueryOptions(options)

	query := DB.Table("user_proxy_filter_indexes ufi").
		Select(
			"ufi.proxy_id AS id, "+
				"ufi.ip AS ip_encrypted, "+
				"ufi.port AS port, "+
				"ufi.estimated_type AS estimated_type, "+
				"ufi.response_time AS response_time, "+
				"ufi.country AS country, "+
				"ufi.anonymity_level AS anonymity_level, "+
				"ufi.alive AS alive, "+
				"ufi.health_overall AS health_overall, "+
				"ufi.health_http AS health_http, "+
				"ufi.health_https AS health_https, "+
				"ufi.health_socks4 AS health_socks4, "+
				"ufi.health_socks5 AS health_socks5, "+
				"ufi.latest_check AS latest_check",
		).
		Where("ufi.user_id = ?", userId)

	query = applyProxyPageSort(query, options)

	filterQuery := buildProxyListFilterQuery(userId, filters)
	if filterQuery != nil {
		query = query.Where("ufi.proxy_id IN (?)", filterQuery)
	}

	rows := make([]dto.ProxyInfoRow, 0)
	normalizedSearch := strings.TrimSpace(search)
	lowerSearch := strings.ToLower(normalizedSearch)

	if normalizedSearch == "" {
		offset := (page - 1) * pageSize
		query = query.Offset(offset).Limit(pageSize)
		if err := query.Scan(&rows).Error; err != nil {
			return []dto.ProxyInfo{}, 0
		}

		proxies := proxyInfoRowsToDTO(rows)
		if options.IncludeReputation {
			attachReputationsToProxyInfos(proxies)
		}
		if filterQuery == nil {
			total := GetAllProxyCountOfUser(userId)
			return proxies, total
		}

		var total int64
		if err := DB.Table("(?) AS filtered", filterQuery.Select("ufi.proxy_id").Group("ufi.proxy_id")).Count(&total).Error; err != nil {
			return proxies, 0
		}
		return proxies, total
	}

	if !isLikelyProxyIPSearch(lowerSearch) {
		matchedProxyIDs := buildProxySearchIDQuery(userId, filterQuery, lowerSearch)

		var total int64
		if err := DB.Table("(?) AS matched", matchedProxyIDs).Count(&total).Error; err != nil {
			return []dto.ProxyInfo{}, 0
		}

		if total == 0 {
			return []dto.ProxyInfo{}, 0
		}

		offset := (page - 1) * pageSize
		if err := query.
			Joins("JOIN (?) AS matched ON matched.id = ufi.proxy_id", matchedProxyIDs).
			Offset(offset).
			Limit(pageSize).
			Scan(&rows).Error; err != nil {
			return []dto.ProxyInfo{}, 0
		}

		proxies := proxyInfoRowsToDTO(rows)
		if options.IncludeReputation {
			attachReputationsToProxyInfos(proxies)
		}
		return proxies, total
	}

	rangeStart, rangeEnd, ok := buildIPIntSearchRange(lowerSearch)
	if !ok {
		return []dto.ProxyInfo{}, 0
	}

	matchedProxyIDs := buildProxyIPSearchIDQuery(userId, filterQuery, rangeStart, rangeEnd)

	var total int64
	if err := DB.Table("(?) AS matched", matchedProxyIDs).Count(&total).Error; err != nil {
		return []dto.ProxyInfo{}, 0
	}
	if total == 0 {
		return []dto.ProxyInfo{}, 0
	}

	offset := (page - 1) * pageSize
	if err := query.
		Joins("JOIN (?) AS matched ON matched.id = ufi.proxy_id", matchedProxyIDs).
		Offset(offset).
		Limit(pageSize).
		Scan(&rows).Error; err != nil {
		return []dto.ProxyInfo{}, 0
	}

	proxies := proxyInfoRowsToDTO(rows)
	if options.IncludeReputation {
		attachReputationsToProxyInfos(proxies)
	}
	return proxies, total
}

func normalizeProxyPageQueryOptions(options ProxyPageQueryOptions) ProxyPageQueryOptions {
	sortField := normalizeProxyPageSortField(options.SortField)
	sortOrder := normalizeProxyPageSortOrder(options.SortOrder)
	if sortField == "" || sortOrder == "" {
		sortField = ""
		sortOrder = ""
	}

	return ProxyPageQueryOptions{
		IncludeHealth:     options.IncludeHealth || proxyPageSortNeedsHealth(sortField),
		IncludeReputation: options.IncludeReputation || sortField == "reputation",
		SortField:         sortField,
		SortOrder:         sortOrder,
	}
}

func normalizeProxyPageSortField(field string) string {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "alive":
		return "alive"
	case "health_overall", "alive_ratio_overall":
		return "health_overall"
	case "health_http", "alive_ratio_http":
		return "health_http"
	case "health_https", "alive_ratio_https":
		return "health_https"
	case "health_socks4", "alive_ratio_socks4":
		return "health_socks4"
	case "health_socks5", "alive_ratio_socks5":
		return "health_socks5"
	case "ip":
		return "ip"
	case "ip_port":
		return "ip_port"
	case "port":
		return "port"
	case "response_time":
		return "response_time"
	case "estimated_type":
		return "estimated_type"
	case "country":
		return "country"
	case "reputation":
		return "reputation"
	case "latest_check":
		return "latest_check"
	default:
		return ""
	}
}

func normalizeProxyPageSortOrder(order string) string {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "asc", "1":
		return "asc"
	case "desc", "-1":
		return "desc"
	default:
		return ""
	}
}

func proxyPageSortNeedsHealth(field string) bool {
	return field == "health_overall" ||
		field == "health_http" ||
		field == "health_https" ||
		field == "health_socks4" ||
		field == "health_socks5"
}

func applyProxyPageSort(query *gorm.DB, options ProxyPageQueryOptions) *gorm.DB {
	if options.SortField == "" || options.SortOrder == "" {
		return query.Order("alive DESC, latest_check DESC")
	}

	direction := "ASC"
	if options.SortOrder == "desc" {
		direction = "DESC"
	}

	expressions := proxyPageSortExpressions(options.SortField)
	if len(expressions) == 0 {
		return query.Order("alive DESC, latest_check DESC")
	}

	orderParts := make([]string, 0, len(expressions)+1)
	for _, expression := range expressions {
		orderParts = append(orderParts, fmt.Sprintf("%s %s NULLS LAST", expression, direction))
	}
	orderParts = append(orderParts, "ufi.proxy_id ASC")

	return query.Order(strings.Join(orderParts, ", "))
}

func proxyPageSortExpressions(field string) []string {
	switch field {
	case "alive":
		return []string{"ufi.alive"}
	case "health_overall":
		return []string{"ufi.health_overall"}
	case "health_http":
		return []string{"ufi.health_http"}
	case "health_https":
		return []string{"ufi.health_https"}
	case "health_socks4":
		return []string{"ufi.health_socks4"}
	case "health_socks5":
		return []string{"ufi.health_socks5"}
	case "ip":
		return []string{"ufi.ip_int"}
	case "ip_port":
		return []string{"ufi.ip_int", "ufi.port"}
	case "port":
		return []string{"ufi.port"}
	case "response_time":
		return []string{"ufi.response_time"}
	case "estimated_type":
		return []string{"ufi.estimated_type"}
	case "country":
		return []string{"ufi.country"}
	case "reputation":
		return []string{"ufi.reputation_score"}
	case "latest_check":
		return []string{"ufi.latest_check"}
	default:
		return nil
	}
}

func isLikelyProxyIPSearch(search string) bool {
	if search == "" {
		return false
	}

	for _, r := range search {
		if (r >= '0' && r <= '9') || r == '.' {
			continue
		}
		return false
	}

	return strings.Contains(search, ".")
}

func buildIPIntSearchRange(search string) (uint32, uint32, bool) {
	if search == "" {
		return 0, 0, false
	}

	parts := strings.Split(search, ".")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 || len(parts) > 4 {
		return 0, 0, false
	}

	octets := make([]uint32, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return 0, 0, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return 0, 0, false
		}
		octets = append(octets, uint32(value))
	}

	var start uint32
	var end uint32
	for idx := 0; idx < 4; idx++ {
		shift := uint(24 - (idx * 8))

		if idx < len(octets) {
			start |= octets[idx] << shift
			end |= octets[idx] << shift
			continue
		}

		end |= uint32(255) << shift
	}

	return start, end, true
}

func buildProxySearchIDQuery(userId uint, filterQuery *gorm.DB, lowerSearch string) *gorm.DB {
	query := DB.Table("user_proxy_filter_indexes ufi").
		Select("ufi.proxy_id AS id").
		Where("ufi.user_id = ?", userId)

	if filterQuery != nil {
		query = query.Where("ufi.proxy_id IN (?)", filterQuery)
	}

	query = applyProxySearchQuery(query, lowerSearch)
	return query.Group("ufi.proxy_id")
}

func buildProxyIPSearchIDQuery(userId uint, filterQuery *gorm.DB, rangeStart, rangeEnd uint32) *gorm.DB {
	query := DB.Table("user_proxy_filter_indexes ufi").
		Select("ufi.proxy_id AS id").
		Where("ufi.user_id = ?", userId).
		Where("ufi.ip_int BETWEEN ? AND ?", rangeStart, rangeEnd)

	if filterQuery != nil {
		query = query.Where("ufi.proxy_id IN (?)", filterQuery)
	}

	return query.Group("ufi.proxy_id")
}

func applyProxySearchQuery(query *gorm.DB, lowerSearch string) *gorm.DB {
	whereSQL, args := buildProxySearchPredicate(lowerSearch)
	if whereSQL == "" || len(args) == 0 {
		return query
	}
	return query.Where(whereSQL, args...)
}

func buildProxySearchPredicate(lowerSearch string) (string, []interface{}) {
	lowerSearch = strings.ToLower(strings.TrimSpace(lowerSearch))
	if lowerSearch == "" {
		return "", nil
	}

	pattern := "%" + lowerSearch + "%"
	conditions := []string{
		"ufi.type_key LIKE ?",
		"ufi.country_key LIKE ?",
		"ufi.anonymity_key LIKE ?",
		"ufi.reputation_label LIKE ?",
	}
	args := []interface{}{
		pattern,
		pattern,
		pattern,
		pattern,
	}

	switch lowerSearch {
	case "alive":
		conditions = append(conditions, "ufi.alive = ?")
		args = append(args, true)
	case "dead":
		conditions = append(conditions, "ufi.alive = ?")
		args = append(args, false)
	}

	if numericValue, err := strconv.ParseUint(lowerSearch, 10, 16); err == nil {
		conditions = append(conditions, "ufi.port = ?")
		args = append(args, uint16(numericValue))
		conditions = append(conditions, "ufi.response_time = ?")
		args = append(args, uint16(numericValue))
	}

	return "(" + strings.Join(conditions, " OR ") + ")", args
}

func buildProxyListFilterQuery(userId uint, filters dto.ProxyListFilters) *gorm.DB {
	if !hasProxyListFilters(filters) {
		return nil
	}

	selectedProtocols := normalizeProtocolFilters(filters.Protocols)

	query := DB.Table("user_proxy_filter_indexes ufi").
		Select("ufi.proxy_id").
		Where("ufi.user_id = ?", userId)

	if filters.Status == "alive" || filters.Status == "dead" {
		if filters.Status == "alive" {
			query = query.Where("ufi.alive = ?", true)
		} else {
			query = query.Where("ufi.alive = ?", false)
		}
	}

	if len(filters.Countries) > 0 {
		query = query.Where("ufi.country_key IN ?", filters.Countries)
	}

	if len(filters.Types) > 0 {
		query = query.Where("ufi.type_key IN ?", filters.Types)
	}

	if hasProxyHealthFilters(filters) {
		if filters.MinHealthOverall > 0 {
			query = query.Where("COALESCE(ufi.health_overall, -1) >= ?", filters.MinHealthOverall)
		}

		if filters.MinHealthHTTP > 0 {
			query = query.Where("COALESCE(ufi.health_http, -1) >= ?", filters.MinHealthHTTP)
		}

		if filters.MinHealthHTTPS > 0 {
			query = query.Where("COALESCE(ufi.health_https, -1) >= ?", filters.MinHealthHTTPS)
		}

		if filters.MinHealthSOCKS4 > 0 {
			query = query.Where("COALESCE(ufi.health_socks4, -1) >= ?", filters.MinHealthSOCKS4)
		}

		if filters.MinHealthSOCKS5 > 0 {
			query = query.Where("COALESCE(ufi.health_socks5, -1) >= ?", filters.MinHealthSOCKS5)
		}
	}

	if len(filters.AnonymityLevels) > 0 {
		query = query.Where("ufi.anonymity_key IN ?", filters.AnonymityLevels)
	}

	if filters.MaxTimeout > 0 {
		query = query.Where("ufi.response_time <= ?", filters.MaxTimeout)
	}

	if filters.MaxRetries > 0 {
		query = query.Where("ufi.attempt <= ?", filters.MaxRetries)
	}

	if len(selectedProtocols) > 0 {
		for _, protocol := range selectedProtocols {
			switch protocol {
			case "http":
				query = query.Where("ufi.alive_http = ?", true)
			case "https":
				query = query.Where("ufi.alive_https = ?", true)
			case "socks4":
				query = query.Where("ufi.alive_socks4 = ?", true)
			case "socks5":
				query = query.Where("ufi.alive_socks5 = ?", true)
			}
		}
	}

	if len(filters.ReputationLabels) > 0 {
		query = applyListReputationFilters(query, filters.ReputationLabels, selectedProtocols)
	}

	return query.Group("ufi.proxy_id")
}

func normalizeProtocolFilters(protocols []string) []string {
	if len(protocols) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(protocols))
	out := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		normalized := strings.ToLower(strings.TrimSpace(protocol))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out
}

func hasProxyListFilters(filters dto.ProxyListFilters) bool {
	if filters.Status == "alive" || filters.Status == "dead" {
		return true
	}
	if hasProxyHealthFilters(filters) {
		return true
	}
	if filters.MaxTimeout > 0 || filters.MaxRetries > 0 {
		return true
	}
	if len(filters.Protocols) > 0 || len(filters.Countries) > 0 || len(filters.Types) > 0 || len(filters.AnonymityLevels) > 0 || len(filters.ReputationLabels) > 0 {
		return true
	}
	return false
}

func hasProxyHealthFilters(filters dto.ProxyListFilters) bool {
	return filters.MinHealthOverall > 0 ||
		filters.MinHealthHTTP > 0 ||
		filters.MinHealthHTTPS > 0 ||
		filters.MinHealthSOCKS4 > 0 ||
		filters.MinHealthSOCKS5 > 0
}

func GetProxyFilterOptions(userId uint) (dto.ProxyFilterOptions, error) {
	if DB == nil {
		return dto.ProxyFilterOptions{}, fmt.Errorf("database connection was not initialised")
	}

	countries, err := loadDistinctProxyInfoValue(userId, "ufi.country")
	if err != nil {
		return dto.ProxyFilterOptions{}, err
	}

	types, err := loadDistinctProxyInfoValue(userId, "ufi.estimated_type")
	if err != nil {
		return dto.ProxyFilterOptions{}, err
	}

	anonymityLevels, err := loadDistinctAnonymityLevels(userId)
	if err != nil {
		return dto.ProxyFilterOptions{}, err
	}

	return dto.ProxyFilterOptions{
		Countries:       countries,
		Types:           types,
		AnonymityLevels: anonymityLevels,
	}, nil
}

type proxyFilterValueRow struct {
	Value string `gorm:"column:value"`
}

func loadDistinctProxyInfoValue(userId uint, column string) ([]string, error) {
	var rows []proxyFilterValueRow
	if err := DB.Table("user_proxy_filter_indexes ufi").
		Select("DISTINCT COALESCE(NULLIF("+column+", ''), 'N/A') AS value").
		Where("ufi.user_id = ?", userId).
		Order("value").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	return extractProxyFilterValues(rows), nil
}

func loadDistinctAnonymityLevels(userId uint) ([]string, error) {
	var rows []proxyFilterValueRow
	if err := DB.Table("user_proxy_filter_indexes ufi").
		Select("DISTINCT COALESCE(NULLIF(ufi.anonymity_level, ''), 'N/A') AS value").
		Where("ufi.user_id = ?", userId).
		Order("value").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	return extractProxyFilterValues(rows), nil
}

func extractProxyFilterValues(rows []proxyFilterValueRow) []string {
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.Value) == "" {
			continue
		}
		values = append(values, row.Value)
	}
	return values
}

func proxyInfoRowsToDTO(rows []dto.ProxyInfoRow) []dto.ProxyInfo {
	results := make([]dto.ProxyInfo, 0, len(rows))
	for _, row := range rows {
		ip, _, err := security.DecryptProxySecret(row.IPEncrypted)
		if err != nil {
			log.Errorf("decrypt proxy ip: %v", err)
			ip = ""
		}

		results = append(results, dto.ProxyInfo{
			Id:             row.Id,
			IP:             ip,
			Port:           row.Port,
			EstimatedType:  row.EstimatedType,
			ResponseTime:   row.ResponseTime,
			Country:        row.Country,
			AnonymityLevel: row.AnonymityLevel,
			Alive:          row.Alive,
			Health:         buildHealthSummary(row),
			LatestCheck:    row.LatestCheck,
		})
	}

	return results
}

func nullFloat32Pointer(value sql.NullFloat64) *float32 {
	if !value.Valid {
		return nil
	}

	return new(float32(value.Float64))
}

func buildHealthSummary(row dto.ProxyInfoRow) *dto.ProxyHealthSummary {
	summary := &dto.ProxyHealthSummary{
		Overall: nullFloat32Pointer(row.HealthOverall),
		HTTP:    nullFloat32Pointer(row.HealthHTTP),
		HTTPS:   nullFloat32Pointer(row.HealthHTTPS),
		SOCKS4:  nullFloat32Pointer(row.HealthSOCKS4),
		SOCKS5:  nullFloat32Pointer(row.HealthSOCKS5),
	}

	if summary.Overall == nil && summary.HTTP == nil && summary.HTTPS == nil && summary.SOCKS4 == nil && summary.SOCKS5 == nil {
		return nil
	}

	return summary
}

func filterProxiesBySearch(proxies []dto.ProxyInfo, search string) []dto.ProxyInfo {
	if search == "" {
		return proxies
	}

	filtered := make([]dto.ProxyInfo, 0, len(proxies))
	for _, proxy := range proxies {
		if proxyMatchesSearch(proxy, search) {
			filtered = append(filtered, proxy)
		}
	}

	return filtered
}

func proxyMatchesSearch(proxy dto.ProxyInfo, search string) bool {
	lowerSearch := strings.ToLower(strings.TrimSpace(search))
	if lowerSearch == "" {
		return true
	}

	ipLower := strings.ToLower(proxy.IP)
	if strings.Contains(ipLower, lowerSearch) || strings.Contains(lowerSearch, ipLower) {
		return true
	}

	portStr := strconv.FormatUint(uint64(proxy.Port), 10)
	if strings.Contains(portStr, lowerSearch) || strings.Contains(lowerSearch, portStr) {
		return true
	}

	fields := []string{
		strings.ToLower(proxy.EstimatedType),
		strings.ToLower(proxy.Country),
		strings.ToLower(proxy.AnonymityLevel),
	}

	for _, field := range fields {
		if field == "" {
			continue
		}
		if strings.Contains(field, lowerSearch) || strings.Contains(lowerSearch, field) {
			return true
		}
	}

	responseStr := strconv.Itoa(int(proxy.ResponseTime))
	if strings.Contains(responseStr, lowerSearch) || strings.Contains(lowerSearch, responseStr) {
		return true
	}

	aliveLabel := "dead"
	if proxy.Alive {
		aliveLabel = "alive"
	}
	if strings.Contains(aliveLabel, lowerSearch) || strings.Contains(lowerSearch, aliveLabel) {
		return true
	}

	if !proxy.LatestCheck.IsZero() {
		timestamp := strings.ToLower(proxy.LatestCheck.Format(time.RFC3339))
		if strings.Contains(timestamp, lowerSearch) || strings.Contains(lowerSearch, timestamp) {
			return true
		}
	}

	if proxy.Reputation != nil {
		if proxy.Reputation.Overall != nil {
			label := strings.ToLower(strings.TrimSpace(proxy.Reputation.Overall.Label))
			if label != "" && (strings.Contains(label, lowerSearch) || strings.Contains(lowerSearch, label)) {
				return true
			}

			score := strings.TrimSpace(strconv.FormatFloat(float64(proxy.Reputation.Overall.Score), 'f', -1, 32))
			if score != "" && (strings.Contains(score, lowerSearch) || strings.Contains(lowerSearch, score)) {
				return true
			}
		}

		for protocol, rep := range proxy.Reputation.Protocols {
			protocolLower := strings.ToLower(strings.TrimSpace(protocol))
			if protocolLower != "" && (strings.Contains(protocolLower, lowerSearch) || strings.Contains(lowerSearch, protocolLower)) {
				return true
			}

			label := strings.ToLower(strings.TrimSpace(rep.Label))
			if label != "" && (strings.Contains(label, lowerSearch) || strings.Contains(lowerSearch, label)) {
				return true
			}

			score := strings.TrimSpace(strconv.FormatFloat(float64(rep.Score), 'f', -1, 32))
			if score != "" && (strings.Contains(score, lowerSearch) || strings.Contains(lowerSearch, score)) {
				return true
			}
		}
	}

	return false
}

func attachReputationsToProxyInfos(proxies []dto.ProxyInfo) {
	if len(proxies) == 0 {
		return
	}

	proxyIDs := make([]uint64, 0, len(proxies))
	for _, proxy := range proxies {
		if proxy.Id <= 0 {
			continue
		}
		proxyIDs = append(proxyIDs, uint64(proxy.Id))
	}

	if len(proxyIDs) == 0 {
		return
	}

	repMap, err := GetProxyReputations(context.Background(), proxyIDs)
	if err != nil {
		log.Error("failed to load proxy reputations", "error", err)
		return
	}

	missing := make([]uint64, 0)
	dedupMissing := make(map[uint64]struct{}, len(proxies))

	for index := range proxies {
		id := uint64(proxies[index].Id)
		if rows, ok := repMap[id]; ok && len(rows) > 0 {
			proxies[index].Reputation = mapReputationsToSummary(rows)
			continue
		}

		if id == 0 {
			continue
		}

		if _, seen := dedupMissing[id]; !seen {
			dedupMissing[id] = struct{}{}
			missing = append(missing, id)
		}
	}

	if len(missing) > 0 {
		scheduleReputationRecalculation(missing)
	}
}

func mapReputationsToSummary(rows []domain.ProxyReputation) *dto.ProxyReputationSummary {
	if len(rows) == 0 {
		return nil
	}

	summary := &dto.ProxyReputationSummary{
		Protocols: make(map[string]dto.ProxyReputation),
	}

	for _, row := range rows {
		rep := dto.ProxyReputation{
			Kind:  row.Kind,
			Score: row.Score,
			Label: row.Label,
		}

		if row.Kind == domain.ProxyReputationKindOverall {
			summary.Overall = new(rep)
		} else {
			summary.Protocols[row.Kind] = rep
		}
	}

	if len(summary.Protocols) == 0 {
		summary.Protocols = nil
	}

	return summary
}

func mapReputationsToBreakdown(rows []domain.ProxyReputation) *dto.ProxyReputationBreakdown {
	if len(rows) == 0 {
		return nil
	}

	breakdown := &dto.ProxyReputationBreakdown{
		Protocols: make(map[string]dto.ProxyReputationDetail),
	}

	for _, row := range rows {
		signals := decodeReputationSignals(row.Signals)
		rep := dto.ProxyReputationDetail{
			Kind:    row.Kind,
			Score:   row.Score,
			Label:   row.Label,
			Signals: signals,
		}

		if row.Kind == domain.ProxyReputationKindOverall {
			breakdown.Overall = new(rep)
		} else {
			breakdown.Protocols[row.Kind] = rep
		}
	}

	if len(breakdown.Protocols) == 0 {
		breakdown.Protocols = nil
	}

	return breakdown
}

func decodeReputationSignals(payload []byte) map[string]any {
	if len(payload) == 0 {
		return nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		log.Error("failed to decode reputation signals", "error", err)
		return nil
	}

	return decoded
}

func scheduleReputationRecalculation(proxyIDs []uint64) {
	if len(proxyIDs) == 0 {
		return
	}

	ids := append([]uint64(nil), proxyIDs...)

	go func(values []uint64) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := RecalculateProxyReputations(ctx, values); err != nil {
			log.Error("failed to backfill proxy reputations", "error", err, "proxy_ids", values)
		}
	}(ids)
}

func GetProxyDetail(userId uint, proxyId uint64) (*dto.ProxyDetail, error) {
	if proxyId == 0 {
		return nil, nil
	}

	var proxy domain.Proxy
	err := DB.
		Preload("Statistics", func(db *gorm.DB) *gorm.DB {
			return db.
				Order("created_at DESC").
				Limit(1).
				Preload("Protocol").
				Preload("Level").
				Preload("Judge")
		}).
		Preload("Reputations").
		Joins("JOIN user_proxies up ON up.proxy_id = proxies.id").
		Where("up.user_id = ? AND proxies.id = ?", userId, proxyId).
		First(&proxy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var latestStat *dto.ProxyStatistic
	var latestCheck *time.Time
	if len(proxy.Statistics) > 0 {
		latestStat = new(mapProxyStatistic(&proxy.Statistics[0]))
		latestCheck = &proxy.Statistics[0].CreatedAt
	}

	detail := &dto.ProxyDetail{
		Id:              int(proxy.ID),
		IP:              proxy.GetIp(),
		Port:            proxy.Port,
		Username:        proxy.Username,
		Password:        proxy.Password,
		HasAuth:         proxy.HasAuth(),
		EstimatedType:   normaliseDisplayValue(proxy.EstimatedType, "N/A"),
		Country:         normaliseDisplayValue(proxy.Country, "Unknown"),
		CreatedAt:       proxy.CreatedAt,
		LatestCheck:     latestCheck,
		LatestStatistic: latestStat,
	}

	detail.Reputation = mapReputationsToBreakdown(proxy.Reputations)

	return detail, nil
}

func GetQueuedProxyForUser(userId uint, proxyId uint64) (*domain.Proxy, error) {
	if proxyId == 0 {
		return nil, nil
	}

	var proxy domain.Proxy
	err := DB.
		Preload("Users", preloadCheckerUsers).
		Joins("JOIN user_proxies up ON up.proxy_id = proxies.id").
		Where("up.user_id = ? AND proxies.id = ?", userId, proxyId).
		First(&proxy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &proxy, nil
}

func GetProxyStatistics(userId uint, proxyId uint64, limit int) ([]dto.ProxyStatistic, error) {
	if proxyId == 0 {
		return []dto.ProxyStatistic{}, nil
	}

	if limit <= 0 || limit > 500 {
		limit = 500
	}

	query := DB.Model(&domain.ProxyStatistic{}).
		Preload("Protocol").
		Preload("Level").
		Preload("Judge").
		Joins("JOIN user_proxies up ON up.proxy_id = proxy_statistics.proxy_id").
		Where("proxy_statistics.proxy_id = ? AND up.user_id = ?", proxyId, userId).
		Order("proxy_statistics.created_at DESC").
		Limit(limit)

	rows := make([]domain.ProxyStatistic, 0, limit)
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}

	stats := make([]dto.ProxyStatistic, len(rows))
	for index := range rows {
		stats[index] = mapProxyStatistic(&rows[index])
	}

	return stats, nil
}

type proxyStatisticBodyRow struct {
	ResponseBody string
	Regex        sql.NullString
}

func GetProxyStatisticResponseBody(userId uint, proxyId uint64, statisticId uint64) (dto.ProxyStatisticDetail, error) {
	if proxyId == 0 || statisticId == 0 {
		return dto.ProxyStatisticDetail{}, gorm.ErrRecordNotFound
	}

	var row proxyStatisticBodyRow
	err := DB.Table("proxy_statistics").
		Select("proxy_statistics.response_body", "user_judges.regex").
		Joins("JOIN user_proxies up ON up.proxy_id = proxy_statistics.proxy_id").
		Joins("LEFT JOIN user_judges ON user_judges.judge_id = proxy_statistics.judge_id AND user_judges.user_id = up.user_id").
		Where("proxy_statistics.id = ? AND proxy_statistics.proxy_id = ? AND up.user_id = ?", statisticId, proxyId, userId).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dto.ProxyStatisticDetail{}, gorm.ErrRecordNotFound
		}
		return dto.ProxyStatisticDetail{}, err
	}

	regex := ""
	if row.Regex.Valid {
		regex = strings.TrimSpace(row.Regex.String)
	}

	return dto.ProxyStatisticDetail{
		ResponseBody: row.ResponseBody,
		Regex:        regex,
	}, nil
}

func mapProxyStatistic(stat *domain.ProxyStatistic) dto.ProxyStatistic {
	if stat == nil {
		return dto.ProxyStatistic{}
	}

	protocol := normaliseDisplayValue(stat.Protocol.Name, "Unknown")
	anonymity := normaliseDisplayValue(stat.Level.Name, "Unknown")
	judge := normaliseDisplayValue(stat.Judge.FullString, "Unknown")

	return dto.ProxyStatistic{
		Id:             stat.ID,
		Alive:          stat.Alive,
		Attempt:        stat.Attempt,
		ResponseTime:   stat.ResponseTime,
		ResponseBody:   stat.ResponseBody,
		Protocol:       protocol,
		AnonymityLevel: anonymity,
		Judge:          judge,
		CreatedAt:      stat.CreatedAt,
	}
}

func normaliseDisplayValue(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func DeleteProxyRelation(userId uint, proxies []int) (int64, []domain.Proxy, error) {
	if len(proxies) == 0 {
		return 0, nil, nil
	}

	var totalDeleted int64
	chunkSize := deleteChunkSize
	if chunkSize > len(proxies) {
		chunkSize = len(proxies)
	}
	if chunkSize <= 0 {
		chunkSize = len(proxies)
	}

	orphanSet := make(map[uint64]struct{})

	for start := 0; start < len(proxies); start += chunkSize {
		end := start + chunkSize
		if end > len(proxies) {
			end = len(proxies)
		}

		chunk := proxies[start:end]
		result := DB.
			Where("user_id = ?", userId).
			Where("proxy_id IN ?", chunk).
			Delete(&domain.UserProxy{})

		if result.Error != nil {
			return totalDeleted, nil, result.Error
		}

		proxyIDs := make([]uint64, 0, len(chunk))
		for _, id := range chunk {
			if id > 0 {
				proxyIDs = append(proxyIDs, uint64(id))
			}
		}
		if len(proxyIDs) > 0 {
			if DB.Migrator().HasTable(&domain.UserProxyFilterIndex{}) {
				if err := DB.
					Where("user_id = ?", userId).
					Where("proxy_id IN ?", proxyIDs).
					Delete(&domain.UserProxyFilterIndex{}).Error; err != nil {
					return totalDeleted, nil, err
				}
			}
			if err := refreshUserScrapeSourceStatsForUserProxyIDs(DB, userId, proxyIDs); err != nil {
				return totalDeleted, nil, err
			}
		}

		totalDeleted += result.RowsAffected

		orphanIDs, err := collectOrphanProxyIDs(chunk)
		if err != nil {
			return totalDeleted, nil, err
		}
		if len(orphanIDs) == 0 {
			continue
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

	var orphans []domain.Proxy
	if err := DB.Where("id IN ?", uniqueIDs).Find(&orphans).Error; err != nil {
		return totalDeleted, nil, err
	}

	return totalDeleted, orphans, nil
}

func CleanupAutoRemovalViolations(ctx context.Context) (int64, []domain.Proxy, error) {
	if DB == nil {
		return 0, nil, fmt.Errorf("database not initialised")
	}

	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}

	type target struct {
		UserID  uint
		ProxyID uint64
	}

	var (
		batch     []target
		total     int64
		orphaned  = make(map[uint64]domain.Proxy)
		queryBase = db.Table("user_proxies up").
				Select("up.user_id, up.proxy_id").
				Joins("JOIN users u ON u.id = up.user_id").
				Where("u.auto_remove_failing_proxies = ?", true).
				Where("u.auto_remove_failure_threshold > 0").
				Where("up.consecutive_failures >= u.auto_remove_failure_threshold")
	)

	result := queryBase.FindInBatches(&batch, autoRemoveCleanupBatchSize, func(tx *gorm.DB, _ int) error {
		if len(batch) == 0 {
			return nil
		}

		perUser := make(map[uint][]int)
		for _, item := range batch {
			perUser[item.UserID] = append(perUser[item.UserID], int(item.ProxyID))
		}

		for userID, proxyIDs := range perUser {
			removed, orphanList, err := DeleteProxyRelation(userID, proxyIDs)
			if err != nil {
				return err
			}
			total += removed
			for _, proxy := range orphanList {
				orphaned[proxy.ID] = proxy
			}
		}

		return nil
	})
	if result.Error != nil {
		return 0, nil, result.Error
	}

	orphanList := make([]domain.Proxy, 0, len(orphaned))
	for _, proxy := range orphaned {
		orphanList = append(orphanList, proxy)
	}

	return total, orphanList, nil
}

func CleanupProxyLimitViolations(ctx context.Context) (int64, []domain.Proxy, error) {
	limitCfg := config.GetConfig().ProxyLimits
	return cleanupProxyLimitViolationsWithConfig(ctx, limitCfg)
}

func cleanupProxyLimitViolationsWithConfig(ctx context.Context, limitCfg config.ProxyLimitConfig) (int64, []domain.Proxy, error) {
	if !limitCfg.Enabled {
		return 0, nil, nil
	}

	if DB == nil {
		return 0, nil, fmt.Errorf("database not initialised")
	}

	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}

	maxPerUser := int64(limitCfg.MaxPerUser)

	query := db.Table("user_proxies up").
		Select("up.user_id").
		Group("up.user_id").
		Having("COUNT(*) > ?", maxPerUser)
	if limitCfg.ExcludeAdmins {
		query = query.Joins("JOIN users u ON u.id = up.user_id").
			Where("u.role <> ?", "admin")
	}

	var userIDs []uint
	if err := query.Pluck("up.user_id", &userIDs).Error; err != nil {
		return 0, nil, err
	}
	userIDs = normalizeUserIDs(userIDs)
	if len(userIDs) == 0 {
		return 0, nil, nil
	}

	totalRemoved := int64(0)
	orphaned := make(map[uint64]domain.Proxy)

	for _, userID := range userIDs {
		var currentCount int64
		if err := db.Table("user_proxies").
			Where("user_id = ?", userID).
			Count(&currentCount).Error; err != nil {
			return 0, nil, err
		}

		overflow := currentCount - maxPerUser
		if overflow <= 0 {
			continue
		}

		var toRemove []uint64
		if err := db.Table("user_proxies").
			Select("proxy_id").
			Where("user_id = ?", userID).
			Order("created_at DESC, proxy_id DESC").
			Limit(int(overflow)).
			Pluck("proxy_id", &toRemove).Error; err != nil {
			return 0, nil, err
		}
		if len(toRemove) == 0 {
			continue
		}

		proxyIDs := make([]int, 0, len(toRemove))
		for _, proxyID := range toRemove {
			proxyIDs = append(proxyIDs, int(proxyID))
		}

		removed, orphanList, err := DeleteProxyRelation(userID, proxyIDs)
		if err != nil {
			return 0, nil, err
		}
		totalRemoved += removed
		for _, proxy := range orphanList {
			orphaned[proxy.ID] = proxy
		}
	}

	orphanList := make([]domain.Proxy, 0, len(orphaned))
	for _, proxy := range orphaned {
		orphanList = append(orphanList, proxy)
	}

	return totalRemoved, orphanList, nil
}

func collectOrphanProxyIDs(candidateIDs []int) ([]uint64, error) {
	if len(candidateIDs) == 0 {
		return nil, nil
	}

	var stillInUse []int
	if err := DB.Model(&domain.UserProxy{}).
		Where("proxy_id IN ?", candidateIDs).
		Distinct("proxy_id").
		Pluck("proxy_id", &stillInUse).Error; err != nil {
		return nil, err
	}

	inUseSet := make(map[int]struct{}, len(stillInUse))
	for _, id := range stillInUse {
		inUseSet[id] = struct{}{}
	}

	seen := make(map[int]struct{}, len(candidateIDs))
	orphanIDs := make([]uint64, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		if _, alreadySeen := seen[id]; alreadySeen {
			continue
		}
		seen[id] = struct{}{}

		if _, inUse := inUseSet[id]; inUse {
			continue
		}

		orphanIDs = append(orphanIDs, uint64(id))
	}

	if len(orphanIDs) == 0 {
		return nil, nil
	}

	return orphanIDs, nil
}

func GetExistingProxyIDSet(ctx context.Context, proxyIDs []uint64) (map[uint64]struct{}, error) {
	if len(proxyIDs) == 0 {
		return nil, nil
	}
	if DB == nil {
		return nil, fmt.Errorf("database not initialised")
	}

	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}

	result := make(map[uint64]struct{})
	for start := 0; start < len(proxyIDs); start += maxParamsPerBatch {
		end := start + maxParamsPerBatch
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}

		var ids []uint64
		if err := db.Model(&domain.Proxy{}).
			Where("id IN ?", proxyIDs[start:end]).
			Pluck("id", &ids).Error; err != nil {
			return nil, err
		}

		for _, id := range ids {
			result[id] = struct{}{}
		}
	}

	return result, nil
}

func DeleteOrphanProxies(ctx context.Context) (int64, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialised")
	}
	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}

	result := db.
		Where("NOT EXISTS (SELECT 1 FROM user_proxies up WHERE up.proxy_id = proxies.id)").
		Delete(&domain.Proxy{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func DeleteProxiesWithSettings(userID uint, settings dto.DeleteSettings) (int64, []domain.Proxy, error) {
	if settings.Scope == "selected" && len(settings.Proxies) == 0 {
		return 0, nil, ErrNoProxiesSelected
	}

	proxyIDs, err := collectProxyIDsForDeletion(userID, settings)
	if err != nil {
		return 0, nil, err
	}

	if len(proxyIDs) == 0 {
		return 0, nil, nil
	}

	intIDs := make([]int, 0, len(proxyIDs))
	for _, id := range proxyIDs {
		intIDs = append(intIDs, int(id))
	}

	return DeleteProxyRelation(userID, intIDs)
}

func StreamProxiesForExport(userID uint, settings dto.ExportSettings, batchSize int, consume func(domain.Proxy) error) error {
	if DB == nil {
		return fmt.Errorf("database connection was not initialised")
	}
	if consume == nil {
		return fmt.Errorf("export consumer callback is nil")
	}
	if batchSize <= 0 {
		batchSize = proxyExportBatchSize
	}

	tx := DB.Begin(&sql.TxOptions{
		ReadOnly:  true,
		Isolation: sql.LevelRepeatableRead,
	})
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if err := tx.Rollback().Error; err != nil && !errors.Is(err, gorm.ErrInvalidTransaction) {
			log.Error("failed to rollback export transaction", "error", err)
		}
	}()

	idQuery := tx.Model(&domain.Proxy{}).
		Select("DISTINCT proxies.id").
		Joins("JOIN user_proxies ON user_proxies.proxy_id = proxies.id").
		Where("user_proxies.user_id = ?", userID)

	if len(settings.Proxies) > 0 {
		idQuery = idQuery.Where("proxies.id IN ?", settings.Proxies)
	}

	if settings.Filter {
		filterQuery := buildProxyListFilterQuery(userID, proxyListFiltersForExport(settings))
		if filterQuery != nil {
			idQuery = idQuery.Where("proxies.id IN (?)", filterQuery)
		}
	}

	var lastID uint64
	for {
		var ids []uint64
		query := idQuery
		if lastID > 0 {
			query = query.Where("proxies.id > ?", lastID)
		}
		if err := query.Order("proxies.id ASC").Limit(batchSize).Pluck("proxies.id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}

		proxies, err := loadExportProxyBatch(tx, ids)
		if err != nil {
			return err
		}

		filtered := filterProxiesForExport(proxies, settings)
		for _, proxy := range filtered {
			if err := consume(proxy); err != nil {
				return err
			}
		}

		lastID = ids[len(ids)-1]
	}

	if err := tx.Commit().Error; err != nil {
		return err
	}

	return nil
}

func proxyListFiltersForExport(settings dto.ExportSettings) dto.ProxyListFilters {
	protocols := make([]string, 0, 4)
	if settings.Http {
		protocols = append(protocols, "http")
	}
	if settings.Https {
		protocols = append(protocols, "https")
	}
	if settings.Socks4 {
		protocols = append(protocols, "socks4")
	}
	if settings.Socks5 {
		protocols = append(protocols, "socks5")
	}

	return dto.ProxyListFilters{
		Status:           settings.ProxyStatus,
		Protocols:        protocols,
		MinHealthOverall: int(settings.MinHealthOverall),
		MinHealthHTTP:    int(settings.MinHealthHTTP),
		MinHealthHTTPS:   int(settings.MinHealthHTTPS),
		MinHealthSOCKS4:  int(settings.MinHealthSOCKS4),
		MinHealthSOCKS5:  int(settings.MinHealthSOCKS5),
		Countries:        normalizeFilterValues(settings.Countries),
		Types:            normalizeFilterValues(settings.Types),
		AnonymityLevels:  normalizeFilterValues(settings.AnonymityLevels),
		MaxTimeout:       int(settings.MaxTimeout),
		MaxRetries:       int(settings.MaxRetries),
		ReputationLabels: settings.ReputationLabels,
	}
}

func normalizeFilterValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.ToLower(strings.TrimSpace(value))
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}

	return normalized
}

func loadExportProxyBatch(tx *gorm.DB, ids []uint64) ([]domain.Proxy, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var proxies []domain.Proxy
	if err := tx.
		Where("id IN ?", ids).
		Find(&proxies).Error; err != nil {
		return nil, err
	}
	if len(proxies) == 0 {
		return nil, nil
	}

	indexByID := make(map[uint64]int, len(proxies))
	for i := range proxies {
		indexByID[proxies[i].ID] = i
	}

	var statRows []struct {
		ProxyID      uint64
		StatisticID  uint64
		Alive        bool
		Attempt      uint8
		ResponseTime uint16
		ProtocolID   int
		ProtocolName string
		CreatedAt    time.Time
	}
	if err := tx.
		Table("proxy_latest_statistics pls").
		Select(
			"pls.proxy_id AS proxy_id, "+
				"ps.id AS statistic_id, ps.alive AS alive, ps.attempt AS attempt, "+
				"ps.response_time AS response_time, ps.protocol_id AS protocol_id, "+
				"COALESCE(proto.name, '') AS protocol_name, ps.created_at AS created_at",
		).
		Joins("JOIN proxy_statistics ps ON ps.id = pls.statistic_id").
		Joins("LEFT JOIN protocols proto ON proto.id = ps.protocol_id").
		Where("pls.proxy_id IN ?", ids).
		Scan(&statRows).Error; err != nil {
		return nil, err
	}

	for _, row := range statRows {
		idx, ok := indexByID[row.ProxyID]
		if !ok {
			continue
		}
		proxies[idx].Statistics = append(proxies[idx].Statistics, domain.ProxyStatistic{
			ID:           row.StatisticID,
			Alive:        row.Alive,
			Attempt:      row.Attempt,
			ResponseTime: row.ResponseTime,
			ProtocolID:   row.ProtocolID,
			Protocol: domain.Protocol{
				ID:   row.ProtocolID,
				Name: row.ProtocolName,
			},
			CreatedAt: row.CreatedAt,
		})
	}

	var reputations []domain.ProxyReputation
	if err := tx.
		Where("proxy_id IN ?", ids).
		Find(&reputations).Error; err != nil {
		return nil, err
	}

	for _, rep := range reputations {
		idx, ok := indexByID[rep.ProxyID]
		if !ok {
			continue
		}
		proxies[idx].Reputations = append(proxies[idx].Reputations, rep)
	}

	ordered := make([]domain.Proxy, 0, len(ids))
	for _, id := range ids {
		idx, ok := indexByID[id]
		if !ok {
			continue
		}
		ordered = append(ordered, proxies[idx])
	}

	return ordered, nil
}

func filterProxiesForExport(proxies []domain.Proxy, settings dto.ExportSettings) []domain.Proxy {
	if len(settings.ReputationLabels) == 0 {
		return proxies
	}

	allowedLabels, includeUnknown := normalizeReputationLabels(settings.ReputationLabels)
	selectedProtocols := protocolsForExport(settings)

	filtered := make([]domain.Proxy, 0, len(proxies))
	for _, proxy := range proxies {
		if proxyMatchesReputationFilters(proxy, allowedLabels, includeUnknown, selectedProtocols) {
			filtered = append(filtered, proxy)
		}
	}

	return filtered
}

func normalizeReputationLabels(labels []string) (map[string]struct{}, bool) {
	allowed := make(map[string]struct{}, len(labels))
	includeUnknown := false

	for _, label := range labels {
		trimmed := strings.ToLower(strings.TrimSpace(label))
		if trimmed == "" {
			continue
		}
		if trimmed == "unknown" {
			includeUnknown = true
			continue
		}
		allowed[trimmed] = struct{}{}
	}

	return allowed, includeUnknown
}

func protocolsForExport(settings dto.ExportSettings) []string {
	if !settings.Filter {
		return nil
	}

	protocols := make([]string, 0, 4)
	if settings.Http {
		protocols = append(protocols, "http")
	}
	if settings.Https {
		protocols = append(protocols, "https")
	}
	if settings.Socks4 {
		protocols = append(protocols, "socks4")
	}
	if settings.Socks5 {
		protocols = append(protocols, "socks5")
	}

	return protocols
}

func applyListReputationFilters(query *gorm.DB, labels []string, protocols []string) *gorm.DB {
	allowedSet, includeUnknown := normalizeReputationLabels(labels)
	if len(allowedSet) == 0 && !includeUnknown {
		return query
	}

	targetKinds := targetReputationKindsForProtocols(protocols)
	if len(targetKinds) == 0 {
		return query
	}

	keys := setToSlice(allowedSet)
	labelExpr := "LOWER(COALESCE(NULLIF(pr.label, ''), 'unknown'))"

	if includeUnknown {
		query = query.Joins("LEFT JOIN proxy_reputations pr ON pr.proxy_id = ufi.proxy_id AND LOWER(pr.kind) IN ?", targetKinds)
		if len(keys) > 0 {
			query = query.Where(labelExpr+" IN ? OR pr.id IS NULL OR "+labelExpr+" = 'unknown'", keys)
		} else {
			query = query.Where("pr.id IS NULL OR " + labelExpr + " = 'unknown'")
		}
	} else {
		query = query.Joins("JOIN proxy_reputations pr ON pr.proxy_id = ufi.proxy_id AND LOWER(pr.kind) IN ?", targetKinds)
		if len(keys) > 0 {
			query = query.Where(labelExpr+" IN ?", keys)
		}
	}

	return query
}

func targetReputationKindsForProtocols(protocols []string) []string {
	if len(protocols) == 0 {
		return []string{domain.ProxyReputationKindOverall}
	}

	out := make([]string, 0, len(protocols))
	for _, proto := range protocols {
		if trimmed := strings.ToLower(strings.TrimSpace(proto)); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	if len(out) == 0 {
		return []string{domain.ProxyReputationKindOverall}
	}

	return out
}

func setToSlice(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func proxyMatchesReputationFilters(proxy domain.Proxy, allowed map[string]struct{}, includeUnknown bool, selectedProtocols []string) bool {
	if len(allowed) == 0 && !includeUnknown {
		return true
	}

	reputations := make(map[string]domain.ProxyReputation, len(proxy.Reputations))
	for _, rep := range proxy.Reputations {
		reputations[strings.ToLower(rep.Kind)] = rep
	}

	targetProtocols := make([]string, 0, len(selectedProtocols))
	for _, proto := range selectedProtocols {
		if trimmed := strings.ToLower(strings.TrimSpace(proto)); trimmed != "" {
			targetProtocols = append(targetProtocols, trimmed)
		}
	}

	if len(targetProtocols) == 0 {
		if len(proxy.Statistics) > 0 {
			proto := strings.ToLower(strings.TrimSpace(proxy.Statistics[0].Protocol.Name))
			if proto != "" {
				targetProtocols = append(targetProtocols, proto)
			}
		}
		if len(targetProtocols) == 0 {
			targetProtocols = append(targetProtocols, domain.ProxyReputationKindOverall)
		}
	}

	for _, proto := range targetProtocols {
		rep, ok := reputations[proto]
		if ok {
			label := strings.ToLower(strings.TrimSpace(rep.Label))
			if label == "" {
				label = "unknown"
			}
			if _, match := allowed[label]; match {
				return true
			}
			if label == "unknown" && includeUnknown {
				return true
			}
			continue
		}

		if includeUnknown {
			return true
		}
	}

	return false
}

package database

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"magpie/internal/api/dto"
	"magpie/internal/config"
	"magpie/internal/domain"
	"magpie/internal/security"
	"magpie/internal/support"
)

type dashboardInfoCacheEntry struct {
	info dto.DashboardInfo
}

var dashboardInfoCache sync.Map

func GetUserFromId(id uint) domain.User {
	var users domain.User
	DB.Where("id = ?", id).First(&users)
	return users
}

func GetUsersByIDsForChecker(ids []uint) (map[uint]domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	unique := make(map[uint]struct{}, len(ids))
	filtered := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := unique[id]; exists {
			continue
		}
		unique[id] = struct{}{}
		filtered = append(filtered, id)
	}

	if len(filtered) == 0 {
		return nil, nil
	}

	var users []domain.User
	if err := DB.Model(&domain.User{}).
		Select(checkerUserSelectColumns).
		Where("id IN ?", filtered).
		Find(&users).Error; err != nil {
		return nil, err
	}

	result := make(map[uint]domain.User, len(users))
	for _, user := range users {
		result[user.ID] = user
	}

	return result, nil
}

func GetUsersThatDontHaveJudges() []domain.User {
	var users []domain.User
	DB.Where("id NOT IN (SELECT DISTINCT user_id FROM user_judges)").Find(&users)
	return users
}

func normalizeDashboardCountry(country string) string {
	trimmed := strings.TrimSpace(country)
	if trimmed == "" {
		return "Unknown"
	}

	switch strings.ToLower(trimmed) {
	case "n/a", "unknown", "unk":
		return "Unknown"
	default:
		return trimmed
	}
}

// AddUserJudgesRelation cannot normally fail because of to many parameters because
// users start with the default judges anyway
func AddUserJudgesRelation(users []domain.User, judges []*domain.JudgeWithRegex) error {
	var userJudges []domain.UserJudge

	for _, user := range users {
		for _, judge := range judges {
			userJudges = append(userJudges, domain.UserJudge{
				UserID:  user.ID,
				JudgeID: judge.Judge.ID,
				Regex:   judge.Regex,
			})
		}
	}

	if len(userJudges) > 0 {
		if err := DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "judge_id"}},
			DoNothing: true,
		}).Create(&userJudges).Error; err != nil {
			return err
		}
	}

	return nil
}

func GetAllUserJudgeRelations() ([]domain.UserJudge, []domain.JudgeWithRegex) {
	var userJudges []domain.UserJudge
	if err := DB.Find(&userJudges).Error; err != nil {
		return nil, nil
	}

	var results []struct {
		ID         uint   `gorm:"column:id"`
		FullString string `gorm:"column:full_string"`
		CreatedAt  time.Time
		Regex      string `gorm:"column:regex"`
	}

	if err := DB.Table("user_judges").
		Select("judges.id, judges.full_string, judges.created_at, user_judges.regex").
		Joins("JOIN judges ON user_judges.judge_id = judges.id").
		Scan(&results).Error; err != nil {
		return nil, nil
	}

	var judgesWithRegex []domain.JudgeWithRegex
	for _, result := range results {
		judge := &domain.Judge{
			ID:         result.ID,
			FullString: result.FullString,
			CreatedAt:  result.CreatedAt,
		}
		judge.SetUp()
		judgesWithRegex = append(judgesWithRegex, domain.JudgeWithRegex{
			Judge: judge,
			Regex: result.Regex,
		})
	}

	return userJudges, judgesWithRegex
}

func UpdateUserSettings(userID uint, settings dto.UserSettings) error {
	// Wrap everything in a single transaction so either all changes
	// happen or none do.
	return DB.Transaction(func(tx *gorm.DB) error {
		transportProtocol := support.NormalizeTransportProtocol(settings.TransportProtocol)

		/* ─── 1.  Update primitive columns on the User row ─────────────────────── */
		updates := map[string]interface{}{
			"HTTPProtocol":               settings.HTTPProtocol,
			"HTTPSProtocol":              settings.HTTPSProtocol,
			"SOCKS4Protocol":             settings.SOCKS4Protocol,
			"SOCKS5Protocol":             settings.SOCKS5Protocol,
			"Timeout":                    settings.Timeout,
			"Retries":                    settings.Retries,
			"UseHttpsForSocks":           settings.UseHttpsForSocks,
			"TransportProtocol":          transportProtocol,
			"AutoRemoveFailingProxies":   settings.AutoRemoveFailingProxies,
			"AutoRemoveFailureThreshold": settings.AutoRemoveFailureThreshold,
			"ProxyListColumns":           domain.StringList(domain.NormalizeProxyListColumns(settings.ProxyListColumns)),
			"ScrapeSourceProxyColumns":   domain.StringList(domain.NormalizeScrapeSourceProxyColumns(settings.ScrapeSourceProxyColumns)),
			"ScrapeSourceListColumns":    domain.StringList(domain.NormalizeScrapeSourceListColumns(settings.ScrapeSourceListColumns)),
		}
		if err := tx.Model(&domain.User{}).
			Where("id = ?", userID).
			Updates(updates).Error; err != nil {
			return err
		}

		desiredByURL := make(map[string]string, len(settings.SimpleUserJudges))
		orderedURLs := make([]string, 0, len(settings.SimpleUserJudges))
		for _, s := range settings.SimpleUserJudges {
			url := strings.TrimSpace(s.Url)
			if url == "" {
				continue
			}
			if config.IsWebsiteBlocked(url) {
				log.Info("Skipped blocked judge for user", "user_id", userID, "url", s.Url)
				continue
			}
			if _, exists := desiredByURL[url]; !exists {
				orderedURLs = append(orderedURLs, url)
			}
			// Preserve legacy behavior where the last repeated URL wins for regex.
			desiredByURL[url] = s.Regex
		}

		if len(orderedURLs) == 0 {
			if err := tx.Where("user_id = ?", userID).Delete(&domain.UserJudge{}).Error; err != nil {
				return err
			}
			return nil
		}

		var existingJudges []domain.Judge
		if err := tx.
			Select("id", "full_string").
			Where("full_string IN ?", orderedURLs).
			Find(&existingJudges).Error; err != nil {
			return err
		}

		existingByURL := make(map[string]uint, len(existingJudges))
		for _, judge := range existingJudges {
			existingByURL[judge.FullString] = judge.ID
		}

		missingJudges := make([]domain.Judge, 0)
		for _, url := range orderedURLs {
			if _, exists := existingByURL[url]; exists {
				continue
			}
			missingJudges = append(missingJudges, domain.Judge{FullString: url})
		}

		if len(missingJudges) > 0 {
			if err := tx.
				Clauses(clause.OnConflict{DoNothing: true}).
				Create(&missingJudges).Error; err != nil {
				return err
			}
		}

		var resolvedJudges []domain.Judge
		if err := tx.
			Select("id", "full_string").
			Where("full_string IN ?", orderedURLs).
			Find(&resolvedJudges).Error; err != nil {
			return err
		}

		judgeIDsByURL := make(map[string]uint, len(resolvedJudges))
		for _, judge := range resolvedJudges {
			judgeIDsByURL[judge.FullString] = judge.ID
		}

		keepIDs := make([]uint, 0, len(orderedURLs))
		userJudges := make([]domain.UserJudge, 0, len(orderedURLs))
		for _, url := range orderedURLs {
			judgeID, ok := judgeIDsByURL[url]
			if !ok || judgeID == 0 {
				continue
			}
			keepIDs = append(keepIDs, judgeID)
			userJudges = append(userJudges, domain.UserJudge{
				UserID:  userID,
				JudgeID: judgeID,
				Regex:   desiredByURL[url],
			})
		}

		if len(userJudges) > 0 {
			if err := tx.
				Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "user_id"}, {Name: "judge_id"}},
					DoUpdates: clause.AssignmentColumns([]string{"regex"}),
				}).
				CreateInBatches(&userJudges, 200).Error; err != nil {
				return err
			}
		}

		if len(keepIDs) == 0 {
			if err := tx.Where("user_id = ?", userID).Delete(&domain.UserJudge{}).Error; err != nil {
				return err
			}
		} else {
			if err := tx.
				Where("user_id = ? AND judge_id NOT IN ?", userID, keepIDs).
				Delete(&domain.UserJudge{}).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

func GetUserJudges(userid uint) []dto.SimpleUserJudge {
	var results []dto.SimpleUserJudge

	if err := DB.Table("user_judges").
		Select("judges.full_string AS Url, user_judges.regex AS Regex").
		Joins("JOIN judges ON user_judges.judge_id = judges.id").
		Where("user_judges.user_id = ?", userid).
		Scan(&results).Error; err != nil {
		return nil
	}

	return results
}

func GetUserJudgesWithRegex(userid uint) ([]domain.JudgeWithRegex, error) {
	var rows []struct {
		ID         uint      `gorm:"column:id"`
		FullString string    `gorm:"column:full_string"`
		CreatedAt  time.Time `gorm:"column:created_at"`
		Regex      string    `gorm:"column:regex"`
	}

	if err := DB.Table("user_judges").
		Select("judges.id, judges.full_string, judges.created_at, user_judges.regex").
		Joins("JOIN judges ON user_judges.judge_id = judges.id").
		Where("user_judges.user_id = ?", userid).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]domain.JudgeWithRegex, 0, len(rows))
	for _, row := range rows {
		judge := &domain.Judge{
			ID:         row.ID,
			FullString: row.FullString,
			CreatedAt:  row.CreatedAt,
		}
		judge.SetUp()
		judge.UpdateIp()
		result = append(result, domain.JudgeWithRegex{
			Judge: judge,
			Regex: row.Regex,
		})
	}

	return result, nil
}

func GetDashboardInfo(userid uint) dto.DashboardInfo {
	if cached, ok := dashboardInfoCache.Load(userid); ok {
		return cached.(dashboardInfoCacheEntry).info
	}

	return RefreshDashboardInfoCache(userid)
}

func RefreshDashboardInfoCache(userid uint) dto.DashboardInfo {
	info := loadDashboardInfo(userid)
	dashboardInfoCache.Store(userid, dashboardInfoCacheEntry{
		info: info,
	})
	return info
}

func RefreshDashboardCaches(ctx context.Context) error {
	if DB == nil {
		return fmt.Errorf("dashboard cache: database connection was not initialised")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var userIDs []uint
	if err := DB.WithContext(ctx).Model(&domain.User{}).Order("id").Pluck("id", &userIDs).Error; err != nil {
		return fmt.Errorf("dashboard cache: load user ids: %w", err)
	}

	for _, userID := range userIDs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			RefreshDashboardInfoCache(userID)
			RefreshRecentProxyChecksCache(userID, defaultRecentProxyChecksLimit)
			RefreshFastestAliveProxiesCache(userID, dashboardFastestAliveLimit)
		}
	}
	return nil
}

func loadDashboardInfo(userid uint) dto.DashboardInfo {
	weekAgo := time.Now().AddDate(0, 0, -7)

	type countryCount struct {
		Country string `gorm:"column:country"`
		Count   uint   `gorm:"column:count"`
	}

	type jvp struct {
		JudgeUrl           string `json:"judge_url"`
		EliteProxies       uint   `json:"elite_proxies"`
		AnonymousProxies   uint   `json:"anonymous_proxies"`
		TransparentProxies uint   `json:"transparent_proxies"`
	}
	type reputationCount struct {
		Label string `gorm:"column:label"`
		Count uint   `gorm:"column:count"`
	}

	type topProxyRow struct {
		ProxyID uint64  `gorm:"column:proxy_id"`
		IP      string  `gorm:"column:ip"`
		Port    uint    `gorm:"column:port"`
		Score   float32 `gorm:"column:score"`
		Label   string  `gorm:"column:label"`
	}

	var (
		checks          dashboardCheckCounts
		scrapedSnapshot proxySnapshotCountSummary
		countries       []countryCount
		judges          []jvp
		repCounts       []reputationCount
		topRow          topProxyRow
		topRowFound     bool
		wg              sync.WaitGroup
	)

	wg.Add(6)
	go func() {
		defer wg.Done()
		var err error
		checks, err = loadDashboardCheckCounts(userid, weekAgo)
		if err == nil {
			return
		}

		log.Error("dashboard: failed to load pre-aggregated check counts", "user_id", userid, "error", err)
		checks, err = loadDashboardCheckCountsDirect(userid, weekAgo)
		if err != nil {
			log.Error("dashboard: failed to load fallback check counts", "user_id", userid, "error", err)
		}
	}()

	go func() {
		defer wg.Done()
		scrapedSnapshot = getProxySnapshotCountSummary(userid, domain.ProxySnapshotMetricScraped, weekAgo)
	}()

	go func() {
		defer wg.Done()
		const countryExpr = "CASE WHEN proxies.country IS NULL OR TRIM(proxies.country) = '' OR LOWER(TRIM(proxies.country)) IN ('n/a', 'unknown', 'unk') THEN 'Unknown' ELSE TRIM(proxies.country) END"
		DB.Model(&domain.Proxy{}).
			Select(countryExpr+" AS country, COUNT(*) AS count").
			Joins("JOIN user_proxies up ON up.proxy_id = proxies.id AND up.user_id = ?", userid).
			Group(countryExpr).
			Order("count DESC, country ASC").
			Scan(&countries)
	}()

	go func() {
		defer wg.Done()
		DB.Table("proxy_latest_statistics pls").
			Select(
				"j.full_string AS judge_url, "+
					"SUM(CASE WHEN al.name = 'elite' THEN 1 ELSE 0 END)       AS elite_proxies, "+
					"SUM(CASE WHEN al.name = 'anonymous' THEN 1 ELSE 0 END)   AS anonymous_proxies, "+
					"SUM(CASE WHEN al.name = 'transparent' THEN 1 ELSE 0 END) AS transparent_proxies",
			).
			Joins("JOIN user_proxies up ON up.proxy_id = pls.proxy_id AND up.user_id = ?", userid).
			Joins("JOIN user_judges uj ON uj.judge_id = pls.judge_id AND uj.user_id = ?", userid).
			Joins("JOIN judges j ON j.id = pls.judge_id").
			Joins("JOIN anonymity_levels al ON al.id = pls.level_id").
			Where("pls.alive = TRUE").
			Group("j.id, j.full_string").
			Scan(&judges)
	}()

	go func() {
		defer wg.Done()
		DB.Table("proxy_reputations AS pr").
			Select("LOWER(COALESCE(NULLIF(pr.label, ''), 'unknown')) AS label, COUNT(*) AS count").
			Joins("JOIN user_proxies up ON up.proxy_id = pr.proxy_id AND up.user_id = ?", userid).
			Where("pr.kind = ?", domain.ProxyReputationKindOverall).
			Group("label").
			Scan(&repCounts)
	}()

	go func() {
		defer wg.Done()
		result := DB.Table("proxy_reputations AS pr").
			Select("pr.proxy_id, pr.score, pr.label, p.ip, p.port").
			Joins("JOIN user_proxies up ON up.proxy_id = pr.proxy_id AND up.user_id = ?", userid).
			Joins("JOIN proxies p ON p.id = pr.proxy_id").
			Where("pr.kind = ?", domain.ProxyReputationKindOverall).
			Order("pr.score DESC, pr.proxy_id ASC").
			Limit(1).
			Scan(&topRow)
		topRowFound = result.Error == nil && result.RowsAffected > 0 && topRow.ProxyID != 0
	}()

	wg.Wait()

	var info dto.DashboardInfo
	info.TotalChecks = checks.TotalChecks
	info.TotalChecksWeek = checks.TotalChecksWeek

	if scrapedSnapshot.Found {
		info.TotalScraped = scrapedSnapshot.Current
		info.TotalScrapedWeek = scrapedSnapshot.Increase
	} else {
		info.TotalScraped = GetCurrentScrapedProxyCount(userid)
	}

	for _, row := range countries {
		info.CountryBreakdown = append(info.CountryBreakdown, struct {
			Country string `json:"country"`
			Count   uint   `json:"count"`
		}{
			Country: normalizeDashboardCountry(row.Country),
			Count:   row.Count,
		})
	}

	for _, row := range judges {
		info.JudgeValidProxies = append(info.JudgeValidProxies, struct {
			JudgeUrl           string `json:"judge_url"`
			EliteProxies       uint   `json:"elite_proxies"`
			AnonymousProxies   uint   `json:"anonymous_proxies"`
			TransparentProxies uint   `json:"transparent_proxies"`
		}{
			JudgeUrl:           row.JudgeUrl,
			EliteProxies:       row.EliteProxies,
			AnonymousProxies:   row.AnonymousProxies,
			TransparentProxies: row.TransparentProxies,
		})
	}

	for _, row := range repCounts {
		switch row.Label {
		case "good":
			info.ReputationBreakdown.Good = row.Count
		case "neutral":
			info.ReputationBreakdown.Neutral = row.Count
		case "poor":
			info.ReputationBreakdown.Poor = row.Count
		default:
			info.ReputationBreakdown.Unknown += row.Count
		}
	}

	if topRowFound {
		ip := topRow.IP
		if ip != "" {
			plain, _, err := security.DecryptProxySecret(ip)
			if err != nil {
				log.Errorf("decrypt top reputation proxy ip: %v", err)
			} else {
				ip = plain
			}
		}

		info.TopReputationProxy = &struct {
			ProxyID uint64  `json:"proxy_id"`
			IP      string  `json:"ip"`
			Port    uint16  `json:"port"`
			Score   float32 `json:"score"`
			Label   string  `json:"label"`
		}{
			ProxyID: topRow.ProxyID,
			IP:      ip,
			Port:    uint16(topRow.Port),
			Score:   topRow.Score,
			Label:   topRow.Label,
		}
	}

	return info
}

func ChangePassword(userID uint, password string) error {
	err := DB.Model(&domain.User{}).Where("ID = ?", userID).Update("password", password).Error
	return err
}

func ChangePasswordIfCurrent(userID uint, expectedPassword string, newPassword string) (bool, error) {
	result := DB.Model(&domain.User{}).
		Where("ID = ? AND password = ?", userID, expectedPassword).
		Update("password", newPassword)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

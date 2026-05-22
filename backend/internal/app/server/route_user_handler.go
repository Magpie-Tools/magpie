package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"magpie/internal/api/dto"
	"magpie/internal/app/bootstrap"
	"magpie/internal/auth"
	"magpie/internal/blacklist"
	"magpie/internal/config"
	"magpie/internal/database"
	"magpie/internal/domain"
	"magpie/internal/jobs/checker/judges"
	proxyqueue "magpie/internal/jobs/queue/proxy"
	sitequeue "magpie/internal/jobs/queue/sites"
	jobruntime "magpie/internal/jobs/runtime"
	"magpie/internal/support"
	"net/http"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

const (
	firstUserAdminAdvisoryLockKey        int64 = 941_843_229_541
	invalidAuthCredentialsMessage              = "Invalid email or password"
	envDisablePublicRegistration               = "DISABLE_PUBLIC_REGISTRATION"
	envEnablePublicFirstAdminBootstrap         = "ENABLE_PUBLIC_FIRST_ADMIN_BOOTSTRAP"
	envAllowInsecureRegistrationDefaults       = "ALLOW_INSECURE_REGISTRATION_DEFAULTS"
)

var (
	errEmailAlreadyInUse          = errors.New("email already in use")
	errPublicRegistrationDisabled = errors.New("public registration is disabled")
	errPublicFirstAdminBootstrap  = errors.New("public first-admin bootstrap is disabled")
	errInvalidOldPassword         = errors.New("invalid old password")
	errHashNewPassword            = errors.New("failed to hash new password")
	errRevokeActiveSessions       = errors.New("failed to revoke active sessions")
	errChangePassword             = errors.New("failed to change password")
	errPasswordRollbackFailed     = errors.New("failed to rollback password after revocation failure")

	loginFallbackPasswordHashOnce sync.Once
	loginFallbackPasswordHash     string

	changePasswordInStore            = database.ChangePassword
	rollbackPasswordIfCurrentInStore = database.ChangePasswordIfCurrent
	revokeUserSessions               = auth.RevokeAllUserJWTs
	validateOutboundConfigURL        = support.ValidateOutboundHTTPURLContext
	runGeoLiteUpdateOnSave           = jobruntime.RunGeoLiteUpdate
	requeueAllQueuedProxies          = func() (int64, error) {
		return proxyqueue.PublicProxyQueue.RequeueAll()
	}
	requeueAllQueuedScrapeSites = func() (int64, error) {
		return sitequeue.PublicScrapeSiteQueue.RequeueAll()
	}
)

type userRegistrationPolicy struct {
	DisablePublicRegistration        bool
	DisablePublicFirstAdminBootstrap bool
}

func checkLogin(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func registerUser(w http.ResponseWriter, r *http.Request) {
	var credentials dto.Credentials
	if !decodeJSONBodyLimited(w, r, &credentials, resolveJSONMaxBodyBytes()) {
		return
	}
	credentials.Email = auth.NormalizeEmail(credentials.Email)

	user := domain.User{
		Email:    credentials.Email,
		Password: credentials.Password,
	}

	// Validate email format
	if !auth.IsValidEmail(user.Email) {
		writeError(w, "Invalid email format", http.StatusBadRequest)
		return
	}

	if err := support.ValidatePassword(user.Password); err != nil {
		writeError(w, support.PasswordValidationMessage(), http.StatusBadRequest)
		return
	}

	// Hash the password
	hashedPassword, err := support.HashPassword(user.Password)
	if err != nil {
		writeError(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}
	user.Password = hashedPassword

	//Set default values
	cfg := config.GetConfig()
	user.HTTPProtocol = cfg.Protocols.HTTP
	user.HTTPSProtocol = cfg.Protocols.HTTPS
	user.SOCKS4Protocol = cfg.Protocols.Socks4
	user.SOCKS5Protocol = cfg.Protocols.Socks5
	user.UseHttpsForSocks = cfg.Checker.UseHttpsForSocks
	user.TransportProtocol = support.TransportTCP

	policy := resolveUserRegistrationPolicy()

	// Save user to the database
	if err = createUserWithFirstAdminRole(&user, policy); err != nil {
		switch {
		case errors.Is(err, errEmailAlreadyInUse):
			writeError(w, "Email already in use", http.StatusConflict)
		case errors.Is(err, errPublicRegistrationDisabled):
			writeError(w, "Public registration is disabled", http.StatusForbidden)
		case errors.Is(err, errPublicFirstAdminBootstrap):
			writeError(w, "Initial admin bootstrap via public registration is disabled", http.StatusForbidden)
		default:
			writeError(w, "Failed to create user", http.StatusInternalServerError)
		}
		return
	}

	go bootstrap.AddDefaultJudgesToUsers()
	registrationWarning := ""
	sites, err := database.SaveScrapingSourcesOfUsers(user.ID, cfg.Scraper.ScrapeSites) // default scrape sites
	if err != nil {
		log.Warn("Could not add default Scraping Sources to user", "err", err)
	} else {
		if err := enqueueScrapeSitesOrRollback(user.ID, sites); err != nil {
			log.Error("Could not queue default scraping sources for user", "user_id", user.ID, "error", err)
			registrationWarning = "Default scrape sources could not be queued and were rolled back. Add sources again later."
		}
	}

	token, err := auth.GenerateJWT(user.ID, user.Role)
	if err != nil {
		writeError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	response := map[string]any{"token": token}
	if registrationWarning != "" {
		response["warning"] = registrationWarning
	}
	writeJSON(w, http.StatusCreated, response)
}

func loginUser(w http.ResponseWriter, r *http.Request) {
	var credentials dto.Credentials
	if !decodeJSONBodyLimited(w, r, &credentials, resolveJSONMaxBodyBytes()) {
		return
	}
	credentials.Email = auth.NormalizeEmail(credentials.Email)

	if blocked, retryAfter := loginFailuresBlocked(r, credentials.Email); blocked {
		setRetryAfterHeader(w, retryAfter)
		recordRateLimitBlockMetric("login_failure")
		writeError(w, authLoginBlockedMessage, http.StatusTooManyRequests)
		return
	}

	var user domain.User
	if err := database.DB.Where("LOWER(email) = ?", credentials.Email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			consumeInvalidPasswordWork(credentials.Password)
			recordLoginFailure(r, credentials.Email)
			recordAuthFailureMetric("invalid_credentials")
			writeError(w, invalidAuthCredentialsMessage, http.StatusUnauthorized)
			return
		}

		writeError(w, "Failed to query database", http.StatusInternalServerError)
		return
	}

	// Compare passwords
	if !support.CheckPasswordHash(credentials.Password, user.Password) {
		recordLoginFailure(r, credentials.Email)
		recordAuthFailureMetric("invalid_credentials")
		writeError(w, invalidAuthCredentialsMessage, http.StatusUnauthorized)
		return
	}

	clearLoginFailures(r, credentials.Email)

	// Generate token
	token, err := auth.GenerateJWT(user.ID, user.Role)
	if err != nil {
		writeError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": token, "role": user.Role})
}

func refreshToken(w http.ResponseWriter, r *http.Request) {
	token, err := auth.ExtractBearerToken(r.Header.Get("Authorization"))
	if err != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rotatedToken, role, err := auth.RotateJWT(token)
	if err != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": rotatedToken, "role": role})
}

func logoutUser(w http.ResponseWriter, r *http.Request) {
	token, err := auth.ExtractBearerToken(r.Header.Get("Authorization"))
	if err != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := auth.RevokeJWT(token); err != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func saveSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	previousCfg := config.GetConfig()

	var newConfig config.Config
	if !decodeJSONBodyLimited(w, r, &newConfig, resolveJSONMaxBodyBytes()) {
		return
	}

	newConfig.WebsiteBlacklist = config.NormalizeWebsiteBlacklist(newConfig.WebsiteBlacklist)
	blockedSet := config.NewWebsiteBlocklistSet(newConfig.WebsiteBlacklist)

	blocked := make(map[string][]string)
	judgeURLs := make([]string, 0, len(newConfig.Checker.Judges))
	for _, j := range newConfig.Checker.Judges {
		if strings.TrimSpace(j.URL) != "" {
			judgeURLs = append(judgeURLs, j.URL)
		}
	}
	if blockedJudges := config.FindBlockedURLs(judgeURLs, blockedSet); len(blockedJudges) > 0 {
		blocked["judges"] = dedupe(blockedJudges)
	}
	if blockedScrape := config.FindBlockedURLs(newConfig.Scraper.ScrapeSites, blockedSet); len(blockedScrape) > 0 {
		blocked["scrape_sites"] = dedupe(blockedScrape)
	}
	if blockedSources := config.FindBlockedURLs(newConfig.BlacklistSources, blockedSet); len(blockedSources) > 0 {
		blocked["blacklist_sources"] = dedupe(blockedSources)
	}
	if blockedLookup := config.FindBlockedURLs([]string{newConfig.Checker.IpLookup}, blockedSet); len(blockedLookup) > 0 {
		blocked["ip_lookup"] = dedupe(blockedLookup)
	}
	unsafeOutbound := make(map[string][]string)
	if invalidSources := findUnsafeOutboundURLs(r.Context(), newConfig.BlacklistSources); len(invalidSources) > 0 {
		unsafeOutbound["blacklist_sources"] = invalidSources
	}
	if invalidLookup := findUnsafeOutboundURLs(r.Context(), []string{newConfig.Checker.IpLookup}); len(invalidLookup) > 0 {
		unsafeOutbound["ip_lookup"] = invalidLookup
	}

	if len(blocked) > 0 || len(unsafeOutbound) > 0 {
		payload := map[string]any{
			"error": "One or more configured URLs are not allowed",
		}
		if len(blocked) > 0 {
			payload["blocked_websites"] = blocked
			payload["website_blacklist"] = newConfig.WebsiteBlacklist
		}
		if len(unsafeOutbound) > 0 {
			payload["unsafe_outbound"] = unsafeOutbound
		}
		writeJSON(w, http.StatusBadRequest, payload)
		return
	}

	if err := config.SetConfig(newConfig); err != nil {
		log.Error("Failed to apply configuration update", "error", err)
		writeError(w, "Failed to update configuration", http.StatusInternalServerError)
		return
	}

	cleanupWarning := ""
	if len(newConfig.WebsiteBlacklist) > 0 {
		cleanupResult, err := database.RemoveBlockedWebsitesFromUsers(context.Background(), newConfig.WebsiteBlacklist)
		if err != nil {
			log.Error("Blocked-website cleanup failed after configuration update", "error", err)
			cleanupWarning = "Configuration was saved, but blocked-site cleanup failed. Retry save after database recovery."
		} else {
			refreshUserJudgeCache(cleanupResult.UpdatedUserJudges)

			if len(cleanupResult.BlockedScrapeSites) > 0 {
				if err := sitequeue.PublicScrapeSiteQueue.RemoveFromQueue(cleanupResult.BlockedScrapeSites); err != nil {
					log.Warn("Failed to remove blocked scrape sites from queue", "error", err, "count", len(cleanupResult.BlockedScrapeSites))
				}
			}

			if cleanupResult.ScrapeRelationsRemoved > 0 {
				if removed, err := database.DeleteOrphanScrapeSites(context.Background()); err != nil {
					log.Warn("Failed to delete orphan scrape sites after blacklist update", "error", err)
				} else if removed > 0 {
					log.Info("Deleted orphan scrape sites after blacklist update", "count", removed)
				}
			}

			if cleanupResult.JudgeRelationsRemoved > 0 || cleanupResult.ScrapeRelationsRemoved > 0 {
				log.Info("Purged blocked websites from users",
					"judge_relations_removed", cleanupResult.JudgeRelationsRemoved,
					"scrape_relations_removed", cleanupResult.ScrapeRelationsRemoved,
					"blocked_sites", len(cleanupResult.BlockedScrapeSites),
				)
			}
		}
	}

	if shouldTriggerGeoLiteUpdateOnSave(previousCfg, newConfig) {
		go runGeoLiteUpdateOnSave(context.Background(), "config-save", true)
	}

	if hasNewBlacklistSources(previousCfg.BlacklistSources, newConfig.BlacklistSources) {
		go blacklist.RunRefresh(context.Background(), "config-save", true)
	}
	response := map[string]any{"message": "Configuration updated successfully"}
	if cleanupWarning != "" {
		response["warning"] = cleanupWarning
	}
	writeJSON(w, http.StatusOK, response)
}

func requeueAllProxies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	count, err := requeueAllQueuedProxies()
	if err != nil {
		log.Error("failed to requeue all proxies", "error", err)
		writeError(w, "Failed to requeue all proxies", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":     "All queued proxies were requeued successfully",
		"proxy_count": count,
	})
}

func requeueAllScrapeSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	count, err := requeueAllQueuedScrapeSites()
	if err != nil {
		log.Error("failed to requeue all scrape sources", "error", err)
		writeError(w, "Failed to requeue all scrape sources", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":      "All queued scrape sources were requeued successfully",
		"source_count": count,
	})
}

func shouldTriggerGeoLiteUpdateOnSave(previousCfg, newConfig config.Config) bool {
	if !newConfig.Plugins.GeoLite.Enabled {
		return false
	}
	previousKey := strings.TrimSpace(previousCfg.Plugins.GeoLite.APIKey)
	newKey := strings.TrimSpace(newConfig.Plugins.GeoLite.APIKey)
	return newKey != "" && (newKey != previousKey || !previousCfg.Plugins.GeoLite.Enabled)
}

func getUserSettings(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user := database.GetUserFromId(userID)
	judges := database.GetUserJudges(userID)
	scrapingSources := database.GetScrapingSourcesOfUsers(userID)

	json.NewEncoder(w).Encode(user.ToUserSettings(judges, scrapingSources))
}

func saveUserSettings(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var userSettings dto.UserSettings
	if !decodeJSONBodyLimited(w, r, &userSettings, resolveJSONMaxBodyBytes()) {
		return
	}

	var blocked []string
	for _, judge := range userSettings.SimpleUserJudges {
		if config.IsWebsiteBlocked(judge.Url) {
			blocked = append(blocked, judge.Url)
		}
	}

	if len(blocked) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":            "One or more judges point to blocked websites",
			"blocked_websites": dedupe(blocked),
		})
		return
	}

	if err := database.UpdateUserSettings(userID, userSettings); err != nil {
		log.Error("failed to update user settings", "user_id", userID, "error", err)
		writeError(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	jwrList, err := database.GetUserJudgesWithRegex(userID)
	if err != nil {
		log.Warn("failed to refresh user judge cache after settings update", "user_id", userID, "error", err)
	} else {
		// atomically replace this user's judges in the global map
		judges.SetUserJudges(userID, jwrList)
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "Settings saved successfully"})
}

func getUserRole(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user := database.GetUserFromId(userID)

	json.NewEncoder(w).Encode(user.Role)
}

func changePassword(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user := database.GetUserFromId(userID)

	var payload dto.ChangePassword
	if !decodeJSONBodyLimited(w, r, &payload, resolveJSONMaxBodyBytes()) {
		return
	}
	if err := support.ValidatePassword(payload.NewPassword); err != nil {
		writeError(w, support.PasswordValidationMessage(), http.StatusBadRequest)
		return
	}

	if err := changePasswordWithSessionRevocation(userID, user.Password, payload); err != nil {
		switch {
		case errors.Is(err, errInvalidOldPassword):
			writeError(w, "Invalid old password", http.StatusUnauthorized)
		case errors.Is(err, errHashNewPassword):
			writeError(w, "Failed to hash password", http.StatusInternalServerError)
		case errors.Is(err, errPasswordRollbackFailed):
			writeError(w, "Failed to revoke active sessions and rollback password state", http.StatusInternalServerError)
		case errors.Is(err, errRevokeActiveSessions):
			writeError(w, "Failed to revoke active sessions; password change was not finalized", http.StatusServiceUnavailable)
		case errors.Is(err, errChangePassword):
			writeError(w, "Failed to change password", http.StatusInternalServerError)
		default:
			writeError(w, "Failed to change password", http.StatusInternalServerError)
		}
		log.Error("change password failed", "user_id", userID, "error", err)
		return
	}

	json.NewEncoder(w).Encode("Password changed successfully")
}

func changePasswordWithSessionRevocation(userID uint, currentPasswordHash string, payload dto.ChangePassword) error {
	if !support.CheckPasswordHash(payload.OldPassword, currentPasswordHash) {
		return errInvalidOldPassword
	}

	hashedPassword, err := support.HashPassword(payload.NewPassword)
	if err != nil {
		return errors.Join(errHashNewPassword, err)
	}

	if err := changePasswordInStore(userID, hashedPassword); err != nil {
		return errors.Join(errChangePassword, err)
	}

	if err := revokeUserSessions(userID); err != nil {
		rolledBack, rollbackErr := rollbackPasswordIfCurrentInStore(userID, hashedPassword, currentPasswordHash)
		if rollbackErr != nil {
			return errors.Join(errRevokeActiveSessions, errPasswordRollbackFailed, err, rollbackErr)
		}
		if !rolledBack {
			log.Warn("password rollback skipped because password changed concurrently", "user_id", userID)
		}
		return errors.Join(errRevokeActiveSessions, err)
	}

	return nil
}

func deleteAccount(w http.ResponseWriter, r *http.Request) {
	userID, userErr := auth.GetUserIDFromRequest(r)
	if userErr != nil {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user := database.GetUserFromId(userID)
	if user.ID == 0 {
		writeError(w, "User not found", http.StatusNotFound)
		return
	}

	var payload dto.DeleteAccount
	if !decodeJSONBodyLimited(w, r, &payload, resolveJSONMaxBodyBytes()) {
		return
	}

	if strings.TrimSpace(payload.Password) == "" {
		writeError(w, "Password is required", http.StatusBadRequest)
		return
	}

	if !support.CheckPasswordHash(payload.Password, user.Password) {
		writeError(w, "Invalid password", http.StatusUnauthorized)
		return
	}

	if err := auth.RevokeAllUserJWTs(userID); err != nil {
		writeError(w, "Failed to revoke active sessions", http.StatusInternalServerError)
		return
	}

	orphanedProxies, orphanedScrapeSites, err := database.DeleteUserAccount(context.Background(), userID)
	if err != nil {
		log.Error("failed to delete user account", "error", err)
		writeError(w, "Failed to delete account", http.StatusInternalServerError)
		return
	}

	judges.SetUserJudges(userID, nil)

	if len(orphanedProxies) > 0 {
		if err := proxyqueue.PublicProxyQueue.RemoveFromQueue(orphanedProxies); err != nil {
			log.Error("failed to remove orphaned proxies from queue", "error", err)
		}
	}

	if len(orphanedScrapeSites) > 0 {
		if err := sitequeue.PublicScrapeSiteQueue.RemoveFromQueue(orphanedScrapeSites); err != nil {
			log.Error("failed to remove orphaned scrape sites from queue", "error", err)
		}
	}

	json.NewEncoder(w).Encode("Account deleted successfully")
}

func createUserWithFirstAdminRole(user *domain.User, policy userRegistrationPolicy) error {
	if user == nil {
		return errors.New("user cannot be nil")
	}

	return database.DB.Transaction(func(tx *gorm.DB) error {
		// Serialize first-user role assignment across instances.
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", firstUserAdminAdvisoryLockKey).Error; err != nil {
				return fmt.Errorf("failed to acquire first-user role lock: %w", err)
			}
		}

		var userCount int64
		if err := tx.Model(&domain.User{}).Count(&userCount).Error; err != nil {
			return err
		}

		if userCount == 0 {
			if policy.DisablePublicFirstAdminBootstrap {
				return errPublicFirstAdminBootstrap
			}
			user.Role = "admin"
		} else {
			if policy.DisablePublicRegistration {
				return errPublicRegistrationDisabled
			}
			user.Role = "user"
		}

		if err := tx.Create(user).Error; err != nil {
			if isUniqueConstraintError(err) {
				return errEmailAlreadyInUse
			}
			return err
		}

		return nil
	})
}

func resolveUserRegistrationPolicy() userRegistrationPolicy {
	disablePublicDefault := true
	if !config.InProductionMode && support.GetEnvBool(envAllowInsecureRegistrationDefaults, false) {
		disablePublicDefault = false
	}

	policy := userRegistrationPolicy{
		DisablePublicRegistration: support.GetEnvBool(envDisablePublicRegistration, disablePublicDefault),
	}

	bootstrapEnabled := support.GetEnvBool(envEnablePublicFirstAdminBootstrap, false)
	policy.DisablePublicFirstAdminBootstrap = !bootstrapEnabled

	return policy
}

func consumeInvalidPasswordWork(password string) {
	fallback := getLoginFallbackPasswordHash()
	if fallback == "" {
		return
	}
	_ = support.CheckPasswordHash(password, fallback)
}

func getLoginFallbackPasswordHash() string {
	loginFallbackPasswordHashOnce.Do(func() {
		hash, err := support.HashPassword("magpie-invalid-login-password")
		if err != nil {
			return
		}
		loginFallbackPasswordHash = hash
	})

	return loginFallbackPasswordHash
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}

	normalized := strings.ToLower(err.Error())
	return strings.Contains(normalized, "duplicate key value violates unique constraint") ||
		strings.Contains(normalized, "unique constraint failed") ||
		strings.Contains(normalized, "duplicate entry")
}

func refreshUserJudgeCache(updates map[uint][]database.UserJudgeAssignment) {
	if len(updates) == 0 {
		return
	}

	for userID, assignments := range updates {
		judgesWithRegex := make([]domain.JudgeWithRegex, 0, len(assignments))

		for _, assignment := range assignments {
			judge := &domain.Judge{
				ID:         assignment.JudgeID,
				FullString: assignment.FullString,
				CreatedAt:  assignment.CreatedAt,
			}

			if err := judge.SetUp(); err != nil {
				log.Warn("Failed to set up judge after website blacklist cleanup", "user_id", userID, "url", assignment.FullString, "error", err)
				continue
			}

			judge.UpdateIp()

			judgesWithRegex = append(judgesWithRegex, domain.JudgeWithRegex{
				Judge: judge,
				Regex: assignment.Regex,
			})
		}

		judges.SetUserJudges(userID, judgesWithRegex)
	}
}

func hasNewBlacklistSources(oldSources, newSources []string) bool {
	existing := make(map[string]struct{}, len(oldSources))
	for _, src := range oldSources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		existing[src] = struct{}{}
	}

	for _, src := range newSources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		if _, ok := existing[src]; !ok {
			return true
		}
	}

	return false
}

func dedupe(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := unique[v]; ok {
			continue
		}
		unique[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func findUnsafeOutboundURLs(ctx context.Context, values []string) []string {
	unsafe := make([]string, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, err := validateOutboundConfigURL(ctx, trimmed); err != nil {
			unsafe = append(unsafe, trimmed)
		}
	}
	return dedupe(unsafe)
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"magpie/internal/api/dto"
	"magpie/internal/config"
	"magpie/internal/database"
	"magpie/internal/domain"
	"magpie/internal/support"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateUserWithFirstAdminRole_AssignsAdminToFirstUser(t *testing.T) {
	setupUserRegistrationTestDB(t)

	user := &domain.User{Email: "first@example.com", Password: "password-hash"}
	err := createUserWithFirstAdminRole(user, userRegistrationPolicy{})
	if err != nil {
		t.Fatalf("createUserWithFirstAdminRole failed: %v", err)
	}

	if user.Role != "admin" {
		t.Fatalf("expected first user role admin, got %q", user.Role)
	}
}

func TestCreateUserWithFirstAdminRole_RespectsPublicRegistrationFlagAfterBootstrap(t *testing.T) {
	setupUserRegistrationTestDB(t)

	admin := &domain.User{Email: "admin@example.com", Password: "password-hash"}
	if err := createUserWithFirstAdminRole(admin, userRegistrationPolicy{}); err != nil {
		t.Fatalf("bootstrap admin failed: %v", err)
	}

	blockedUser := &domain.User{Email: "second@example.com", Password: "password-hash"}
	err := createUserWithFirstAdminRole(blockedUser, userRegistrationPolicy{
		DisablePublicRegistration: true,
	})
	if !errors.Is(err, errPublicRegistrationDisabled) {
		t.Fatalf("expected errPublicRegistrationDisabled, got %v", err)
	}

	allowedUser := &domain.User{Email: "third@example.com", Password: "password-hash"}
	err = createUserWithFirstAdminRole(allowedUser, userRegistrationPolicy{
		DisablePublicRegistration: false,
	})
	if err != nil {
		t.Fatalf("expected follow-up user to be created, got %v", err)
	}
	if allowedUser.Role != "user" {
		t.Fatalf("expected follow-up user role user, got %q", allowedUser.Role)
	}
}

func TestCreateUserWithFirstAdminRole_BlocksFirstAdminBootstrapWhenPolicyDisablesIt(t *testing.T) {
	setupUserRegistrationTestDB(t)

	user := &domain.User{Email: "first@example.com", Password: "password-hash"}
	err := createUserWithFirstAdminRole(user, userRegistrationPolicy{
		DisablePublicFirstAdminBootstrap: true,
	})
	if !errors.Is(err, errPublicFirstAdminBootstrap) {
		t.Fatalf("expected errPublicFirstAdminBootstrap, got %v", err)
	}
}

func TestCreateUserWithFirstAdminRole_AllowsBootstrapWhenEnabled(t *testing.T) {
	setupUserRegistrationTestDB(t)

	user := &domain.User{Email: "first@example.com", Password: "password-hash"}
	err := createUserWithFirstAdminRole(user, userRegistrationPolicy{
		DisablePublicFirstAdminBootstrap: false,
	})
	if err != nil {
		t.Fatalf("expected first admin bootstrap to be accepted, got %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("expected first user role admin, got %q", user.Role)
	}
}

func TestLoginUser_FindsUserCaseInsensitively(t *testing.T) {
	setupUserRegistrationTestDB(t)

	hashedPassword, err := support.HashPassword("Valid-password-123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	user := domain.User{
		Email:    "mixedcase@example.com",
		Password: hashedPassword,
		Role:     "user",
	}
	if err := database.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	body, err := json.Marshal(dto.Credentials{
		Email:    "MixedCase@Example.Com",
		Password: "Valid-password-123",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	loginUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if strings.TrimSpace(payload["token"]) == "" {
		t.Fatal("expected login token in response")
	}
}

func TestSaveSettings_PersistsConfigWhenBlacklistCleanupFails(t *testing.T) {
	withTempServerWorkingDir(t)

	originalCfg := config.GetConfig()
	t.Cleanup(func() {
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	prevDB := database.DB
	database.DB = nil
	t.Cleanup(func() {
		database.DB = prevDB
	})

	newCfg := originalCfg
	newCfg.Protocols.HTTP = !originalCfg.Protocols.HTTP
	newCfg.WebsiteBlacklist = []string{"zz-test-blocked.invalid"}

	body, err := json.Marshal(newCfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/saveSettings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	saveSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	got := config.GetConfig()
	if got.Protocols.HTTP != newCfg.Protocols.HTTP {
		t.Fatalf("config was not persisted despite successful SetConfig: protocol_http=%v want %v", got.Protocols.HTTP, newCfg.Protocols.HTTP)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, ok := payload["warning"]; !ok {
		t.Fatalf("expected warning in response payload when cleanup fails, got: %v", payload)
	}
}

func TestSaveSettings_ReturnsInternalServerErrorWhenSetConfigFails(t *testing.T) {
	withTempServerWorkingDir(t)

	originalCfg := config.GetConfig()
	t.Cleanup(func() {
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	if err := os.MkdirAll("data/settings.json", 0o755); err != nil {
		t.Fatalf("create blocking settings directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll("data/settings.json")
	})

	newCfg := originalCfg
	newCfg.Protocols.HTTP = !originalCfg.Protocols.HTTP
	newCfg.WebsiteBlacklist = nil

	body, err := json.Marshal(newCfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/saveSettings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	saveSettings(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	got := config.GetConfig()
	if got.Protocols.HTTP != originalCfg.Protocols.HTTP {
		t.Fatalf("config was applied despite SetConfig error: protocol_http=%v want %v", got.Protocols.HTTP, originalCfg.Protocols.HTTP)
	}
}

func TestSaveSettings_RejectsUnsafeOutboundConfigTargets(t *testing.T) {
	withTempServerWorkingDir(t)

	originalCfg := config.GetConfig()
	originalValidate := validateOutboundConfigURL
	t.Cleanup(func() {
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
		validateOutboundConfigURL = originalValidate
	})
	validateOutboundConfigURL = func(ctx context.Context, raw string) (*url.URL, error) {
		switch raw {
		case "http://internal.example/blacklist.txt", "http://internal.example/ip":
			return nil, support.ErrUnsafeOutboundTarget
		default:
			return support.ValidateOutboundHTTPURLContext(ctx, raw)
		}
	}

	newCfg := originalCfg
	newCfg.BlacklistSources = []string{"http://internal.example/blacklist.txt"}
	newCfg.Checker.IpLookup = "http://internal.example/ip"

	body, err := json.Marshal(newCfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/saveSettings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	saveSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload["unsafe_outbound"] == nil {
		t.Fatalf("expected unsafe_outbound in response payload, got: %v", payload)
	}

	got := config.GetConfig()
	if got.Checker.IpLookup != originalCfg.Checker.IpLookup {
		t.Fatalf("config was applied despite unsafe ip_lookup: got %q want %q", got.Checker.IpLookup, originalCfg.Checker.IpLookup)
	}
}

func TestSaveSettings_TriggersGeoLiteUpdateWhenAPIKeyChanges(t *testing.T) {
	withTempServerWorkingDir(t)

	originalCfg := config.GetConfig()
	originalRunner := runGeoLiteUpdateOnSave
	t.Cleanup(func() {
		runGeoLiteUpdateOnSave = originalRunner
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	initialCfg := originalCfg
	initialCfg.Plugins.GeoLite.APIKey = ""
	initialCfg.Plugins.GeoLite.Enabled = true
	if err := config.SetConfig(initialCfg); err != nil {
		t.Fatalf("set initial config: %v", err)
	}

	triggered := make(chan string, 1)
	runGeoLiteUpdateOnSave = func(_ context.Context, reason string, force bool) {
		if force {
			triggered <- reason
		}
	}

	newCfg := initialCfg
	newCfg.Protocols.HTTP = !initialCfg.Protocols.HTTP
	newCfg.Plugins.GeoLite.APIKey = "new-key"

	body, err := json.Marshal(newCfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/saveSettings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	saveSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	select {
	case reason := <-triggered:
		if reason != "config-save" {
			t.Fatalf("reason = %q, want %q", reason, "config-save")
		}
	case <-time.After(time.Second):
		t.Fatal("expected GeoLite update trigger when API key changes")
	}
}

func TestSaveSettings_DoesNotTriggerGeoLiteUpdateWhenDisabled(t *testing.T) {
	withTempServerWorkingDir(t)

	originalCfg := config.GetConfig()
	originalRunner := runGeoLiteUpdateOnSave
	t.Cleanup(func() {
		runGeoLiteUpdateOnSave = originalRunner
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	initialCfg := originalCfg
	initialCfg.Plugins.GeoLite.Enabled = true
	initialCfg.Plugins.GeoLite.APIKey = ""
	if err := config.SetConfig(initialCfg); err != nil {
		t.Fatalf("set initial config: %v", err)
	}

	triggered := make(chan string, 1)
	runGeoLiteUpdateOnSave = func(_ context.Context, reason string, force bool) {
		if force {
			triggered <- reason
		}
	}

	newCfg := initialCfg
	newCfg.Plugins.GeoLite.Enabled = false
	newCfg.Plugins.GeoLite.APIKey = "new-key"

	body, err := json.Marshal(newCfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/saveSettings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	saveSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	select {
	case reason := <-triggered:
		t.Fatalf("unexpected GeoLite update trigger with reason %q", reason)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSaveSettings_DoesNotTriggerGeoLiteUpdateWhenAPIKeyUnchanged(t *testing.T) {
	withTempServerWorkingDir(t)

	originalCfg := config.GetConfig()
	originalRunner := runGeoLiteUpdateOnSave
	t.Cleanup(func() {
		runGeoLiteUpdateOnSave = originalRunner
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	initialCfg := originalCfg
	initialCfg.Plugins.GeoLite.APIKey = "same-key"
	if err := config.SetConfig(initialCfg); err != nil {
		t.Fatalf("set initial config: %v", err)
	}

	triggered := make(chan string, 1)
	runGeoLiteUpdateOnSave = func(_ context.Context, reason string, force bool) {
		if force {
			triggered <- reason
		}
	}

	newCfg := initialCfg
	newCfg.Protocols.HTTP = !initialCfg.Protocols.HTTP

	body, err := json.Marshal(newCfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/saveSettings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	saveSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	select {
	case reason := <-triggered:
		t.Fatalf("unexpected GeoLite update trigger with reason %q", reason)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRequeueAllProxies_ReturnsQueuedProxyCount(t *testing.T) {
	originalRequeue := requeueAllQueuedProxies
	t.Cleanup(func() {
		requeueAllQueuedProxies = originalRequeue
	})

	requeueAllQueuedProxies = func() (int64, error) {
		return 42, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/global/proxies/requeue", nil)
	rec := httptest.NewRecorder()

	requeueAllProxies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload["message"] != "All queued proxies were requeued successfully" {
		t.Fatalf("message = %v, want success response", payload["message"])
	}
	if payload["proxy_count"] != float64(42) {
		t.Fatalf("proxy_count = %v, want 42", payload["proxy_count"])
	}
}

func TestRequeueAllProxies_ReturnsInternalServerErrorOnFailure(t *testing.T) {
	originalRequeue := requeueAllQueuedProxies
	t.Cleanup(func() {
		requeueAllQueuedProxies = originalRequeue
	})

	requeueAllQueuedProxies = func() (int64, error) {
		return 0, errors.New("redis unavailable")
	}

	req := httptest.NewRequest(http.MethodPost, "/global/proxies/requeue", nil)
	rec := httptest.NewRecorder()

	requeueAllProxies(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if payload["error"] != "Failed to requeue all proxies" {
		t.Fatalf("error = %q, want failure message", payload["error"])
	}
}

func TestRequeueAllScrapeSources_ReturnsQueuedSourceCount(t *testing.T) {
	originalRequeue := requeueAllQueuedScrapeSites
	t.Cleanup(func() {
		requeueAllQueuedScrapeSites = originalRequeue
	})

	requeueAllQueuedScrapeSites = func() (int64, error) {
		return 17, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/global/scrapeSources/requeue", nil)
	rec := httptest.NewRecorder()

	requeueAllScrapeSources(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload["message"] != "All queued scrape sources were requeued successfully" {
		t.Fatalf("message = %v, want success response", payload["message"])
	}
	if payload["source_count"] != float64(17) {
		t.Fatalf("source_count = %v, want 17", payload["source_count"])
	}
}

func TestRequeueAllScrapeSources_ReturnsInternalServerErrorOnFailure(t *testing.T) {
	originalRequeue := requeueAllQueuedScrapeSites
	t.Cleanup(func() {
		requeueAllQueuedScrapeSites = originalRequeue
	})

	requeueAllQueuedScrapeSites = func() (int64, error) {
		return 0, errors.New("redis unavailable")
	}

	req := httptest.NewRequest(http.MethodPost, "/global/scrapeSources/requeue", nil)
	rec := httptest.NewRecorder()

	requeueAllScrapeSources(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if payload["error"] != "Failed to requeue all scrape sources" {
		t.Fatalf("error = %q, want failure message", payload["error"])
	}
}

func TestResolveUserRegistrationPolicy_ProductionDefaults(t *testing.T) {
	prevProduction := config.InProductionMode
	config.SetProductionMode(true)
	t.Cleanup(func() {
		config.SetProductionMode(prevProduction)
	})

	policy := resolveUserRegistrationPolicy()
	if !policy.DisablePublicRegistration {
		t.Fatal("expected DisablePublicRegistration=true in production by default")
	}
	if !policy.DisablePublicFirstAdminBootstrap {
		t.Fatal("expected DisablePublicFirstAdminBootstrap=true in production by default")
	}
}

func TestResolveUserRegistrationPolicy_ProductionOverrides(t *testing.T) {
	prevProduction := config.InProductionMode
	config.SetProductionMode(true)
	t.Cleanup(func() {
		config.SetProductionMode(prevProduction)
	})

	t.Setenv(envDisablePublicRegistration, "false")
	t.Setenv(envEnablePublicFirstAdminBootstrap, "true")

	policy := resolveUserRegistrationPolicy()
	if policy.DisablePublicRegistration {
		t.Fatal("expected DisablePublicRegistration=false when override is set")
	}
	if policy.DisablePublicFirstAdminBootstrap {
		t.Fatal("expected DisablePublicFirstAdminBootstrap=false when override is set")
	}
}

func TestResolveUserRegistrationPolicy_LocalDefaultsRemainOpen(t *testing.T) {
	prevProduction := config.InProductionMode
	config.SetProductionMode(false)
	t.Cleanup(func() {
		config.SetProductionMode(prevProduction)
	})

	policy := resolveUserRegistrationPolicy()
	if !policy.DisablePublicRegistration {
		t.Fatal("expected DisablePublicRegistration=true by default for safer startup")
	}
	if !policy.DisablePublicFirstAdminBootstrap {
		t.Fatal("expected DisablePublicFirstAdminBootstrap=true by default")
	}
}

func TestResolveUserRegistrationPolicy_LocalOverrideAllowsLegacyOpenDefaults(t *testing.T) {
	prevProduction := config.InProductionMode
	config.SetProductionMode(false)
	t.Cleanup(func() {
		config.SetProductionMode(prevProduction)
	})
	t.Setenv(envAllowInsecureRegistrationDefaults, "true")

	policy := resolveUserRegistrationPolicy()
	if policy.DisablePublicRegistration {
		t.Fatal("expected DisablePublicRegistration=false when insecure local override is enabled")
	}
	if !policy.DisablePublicFirstAdminBootstrap {
		t.Fatal("expected DisablePublicFirstAdminBootstrap=true unless explicitly enabled")
	}
}

func TestResolveUserRegistrationPolicy_BootstrapCanBeEnabledWithoutToken(t *testing.T) {
	prevProduction := config.InProductionMode
	config.SetProductionMode(false)
	t.Cleanup(func() {
		config.SetProductionMode(prevProduction)
	})
	t.Setenv(envEnablePublicFirstAdminBootstrap, "true")

	policy := resolveUserRegistrationPolicy()
	if policy.DisablePublicFirstAdminBootstrap {
		t.Fatal("expected DisablePublicFirstAdminBootstrap=false when explicitly enabled")
	}
}

func TestChangePasswordWithSessionRevocation_RejectsInvalidOldPassword(t *testing.T) {
	originalChange := changePasswordInStore
	originalRollback := rollbackPasswordIfCurrentInStore
	originalRevoke := revokeUserSessions
	t.Cleanup(func() {
		changePasswordInStore = originalChange
		rollbackPasswordIfCurrentInStore = originalRollback
		revokeUserSessions = originalRevoke
	})

	changeCalled := false
	rollbackCalled := false
	revokeCalled := false
	changePasswordInStore = func(userID uint, newPassword string) error {
		changeCalled = true
		return nil
	}
	rollbackPasswordIfCurrentInStore = func(userID uint, expectedPassword string, newPassword string) (bool, error) {
		rollbackCalled = true
		return true, nil
	}
	revokeUserSessions = func(userID uint) error {
		revokeCalled = true
		return nil
	}

	currentHash, err := support.HashPassword("correct-old")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	err = changePasswordWithSessionRevocation(1, currentHash, dto.ChangePassword{
		OldPassword: "wrong-old",
		NewPassword: "New-secret-123",
	})
	if !errors.Is(err, errInvalidOldPassword) {
		t.Fatalf("expected errInvalidOldPassword, got %v", err)
	}
	if revokeCalled {
		t.Fatal("expected revokeUserSessions not to be called for invalid old password")
	}
	if changeCalled {
		t.Fatal("expected changePasswordInStore not to be called for invalid old password")
	}
	if rollbackCalled {
		t.Fatal("expected rollbackPasswordIfCurrentInStore not to be called for invalid old password")
	}
}

func TestChangePasswordWithSessionRevocation_RevocationFailureDoesNotChangePassword(t *testing.T) {
	originalChange := changePasswordInStore
	originalRollback := rollbackPasswordIfCurrentInStore
	originalRevoke := revokeUserSessions
	t.Cleanup(func() {
		changePasswordInStore = originalChange
		rollbackPasswordIfCurrentInStore = originalRollback
		revokeUserSessions = originalRevoke
	})

	currentHash, err := support.HashPassword("correct-old")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	storedHash := currentHash
	revokeUserSessions = func(userID uint) error {
		return errors.New("redis down")
	}
	changePasswordInStore = func(userID uint, newPassword string) error {
		storedHash = newPassword
		return nil
	}
	rollbackPasswordIfCurrentInStore = func(userID uint, expectedPassword string, newPassword string) (bool, error) {
		if storedHash != expectedPassword {
			return false, nil
		}
		storedHash = newPassword
		return true, nil
	}

	err = changePasswordWithSessionRevocation(1, currentHash, dto.ChangePassword{
		OldPassword: "correct-old",
		NewPassword: "New-secret-123",
	})
	if !errors.Is(err, errRevokeActiveSessions) {
		t.Fatalf("expected errRevokeActiveSessions, got %v", err)
	}
	if storedHash != currentHash {
		t.Fatal("expected password to be rolled back to current hash when revocation fails")
	}
}

func TestChangePasswordWithSessionRevocation_RollbackFailureIsSurfaced(t *testing.T) {
	originalChange := changePasswordInStore
	originalRollback := rollbackPasswordIfCurrentInStore
	originalRevoke := revokeUserSessions
	t.Cleanup(func() {
		changePasswordInStore = originalChange
		rollbackPasswordIfCurrentInStore = originalRollback
		revokeUserSessions = originalRevoke
	})

	revokeUserSessions = func(userID uint) error {
		return errors.New("redis down")
	}
	changePasswordInStore = func(userID uint, newPassword string) error {
		return nil
	}
	rollbackPasswordIfCurrentInStore = func(userID uint, expectedPassword string, newPassword string) (bool, error) {
		return false, errors.New("db unavailable")
	}

	currentHash, err := support.HashPassword("correct-old")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	err = changePasswordWithSessionRevocation(1, currentHash, dto.ChangePassword{
		OldPassword: "correct-old",
		NewPassword: "New-secret-123",
	})
	if !errors.Is(err, errPasswordRollbackFailed) {
		t.Fatalf("expected errPasswordRollbackFailed, got %v", err)
	}
}

func TestChangePasswordWithSessionRevocation_RollbackDoesNotClobberConcurrentPasswordChange(t *testing.T) {
	originalChange := changePasswordInStore
	originalRollback := rollbackPasswordIfCurrentInStore
	originalRevoke := revokeUserSessions
	t.Cleanup(func() {
		changePasswordInStore = originalChange
		rollbackPasswordIfCurrentInStore = originalRollback
		revokeUserSessions = originalRevoke
	})

	currentHash, err := support.HashPassword("correct-old")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	competingHash, err := support.HashPassword("competing-secret")
	if err != nil {
		t.Fatalf("hash competing password: %v", err)
	}

	storedHash := currentHash
	revokeUserSessions = func(userID uint) error {
		return errors.New("redis down")
	}
	changePasswordInStore = func(userID uint, newPassword string) error {
		storedHash = newPassword
		return nil
	}
	rollbackPasswordIfCurrentInStore = func(userID uint, expectedPassword string, newPassword string) (bool, error) {
		// Simulate a concurrent successful password change before rollback.
		storedHash = competingHash
		if storedHash != expectedPassword {
			return false, nil
		}
		storedHash = newPassword
		return true, nil
	}

	err = changePasswordWithSessionRevocation(1, currentHash, dto.ChangePassword{
		OldPassword: "correct-old",
		NewPassword: "New-secret-123",
	})
	if !errors.Is(err, errRevokeActiveSessions) {
		t.Fatalf("expected errRevokeActiveSessions, got %v", err)
	}
	if storedHash != competingHash {
		t.Fatal("expected stale rollback to skip and preserve concurrent password change")
	}
}

func TestChangePasswordWithSessionRevocation_ChangesPasswordBeforeRevocation(t *testing.T) {
	originalChange := changePasswordInStore
	originalRollback := rollbackPasswordIfCurrentInStore
	originalRevoke := revokeUserSessions
	t.Cleanup(func() {
		changePasswordInStore = originalChange
		rollbackPasswordIfCurrentInStore = originalRollback
		revokeUserSessions = originalRevoke
	})

	callOrder := make([]string, 0, 2)
	revokeUserSessions = func(userID uint) error {
		callOrder = append(callOrder, "revoke")
		return nil
	}
	changePasswordInStore = func(userID uint, newPassword string) error {
		callOrder = append(callOrder, "change")
		return nil
	}
	rollbackPasswordIfCurrentInStore = func(userID uint, expectedPassword string, newPassword string) (bool, error) {
		callOrder = append(callOrder, "rollback")
		return true, nil
	}

	currentHash, err := support.HashPassword("correct-old")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	err = changePasswordWithSessionRevocation(1, currentHash, dto.ChangePassword{
		OldPassword: "correct-old",
		NewPassword: "New-secret-123",
	})
	if err != nil {
		t.Fatalf("changePasswordWithSessionRevocation returned error: %v", err)
	}
	if len(callOrder) != 2 || callOrder[0] != "change" || callOrder[1] != "revoke" {
		t.Fatalf("unexpected call order: %v", callOrder)
	}
}

func setupUserRegistrationTestDB(t *testing.T) {
	t.Helper()

	prevDB := database.DB
	t.Cleanup(func() {
		database.DB = prevDB
	})

	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if _, err := database.SetupDB(func(cfg *database.Config) {
		cfg.ExistingDB = db
		cfg.AutoMigrate = true
		cfg.Migrations = []any{domain.User{}}
		cfg.SeedDefaults = false
	}); err != nil {
		t.Fatalf("setup db: %v", err)
	}
}

func withTempServerWorkingDir(t *testing.T) {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
}

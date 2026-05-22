package runtime

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"magpie/internal/config"
	"magpie/internal/support"
)

func TestTriggerGeoLiteUpdate_RunsProxyGeoRefreshAfterSuccessfulUpdate(t *testing.T) {
	withTempRuntimeWorkingDir(t)

	originalCfg := config.GetConfig()
	originalUpdate := updateGeoLiteDatabases
	originalRefresh := runProxyGeoRefreshNow
	t.Cleanup(func() {
		updateGeoLiteDatabases = originalUpdate
		runProxyGeoRefreshNow = originalRefresh
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	cfg := originalCfg
	cfg.Plugins.GeoLite.Enabled = true
	cfg.Plugins.GeoLite.APIKey = "test-key"
	cfg.Plugins.GeoLite.AutoUpdate = true
	if err := config.SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}

	updateCalled := false
	refreshCalled := false
	refreshReason := ""
	updateGeoLiteDatabases = func(context.Context) (bool, error) {
		updateCalled = true
		return true, nil
	}
	runProxyGeoRefreshNow = func(_ context.Context, reason string) {
		refreshCalled = true
		refreshReason = reason
	}

	triggerGeoLiteUpdate(context.Background(), "config-save", true)

	if !updateCalled {
		t.Fatal("expected GeoLite updater to run")
	}
	if !refreshCalled {
		t.Fatal("expected proxy geo refresh to run after successful GeoLite update")
	}
	if refreshReason != "geolite-config-save" {
		t.Fatalf("proxy geo refresh reason = %q, want %q", refreshReason, "geolite-config-save")
	}
}

func TestTriggerGeoLiteUpdate_DoesNotRunWhenDisabled(t *testing.T) {
	withTempRuntimeWorkingDir(t)

	originalCfg := config.GetConfig()
	originalUpdate := updateGeoLiteDatabases
	t.Cleanup(func() {
		updateGeoLiteDatabases = originalUpdate
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	cfg := originalCfg
	cfg.Plugins.GeoLite.Enabled = false
	cfg.Plugins.GeoLite.APIKey = "test-key"
	cfg.Plugins.GeoLite.AutoUpdate = true
	if err := config.SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}

	updateCalled := false
	updateGeoLiteDatabases = func(context.Context) (bool, error) {
		updateCalled = true
		return true, nil
	}

	triggerGeoLiteUpdate(context.Background(), "config-save", true)

	if updateCalled {
		t.Fatal("expected GeoLite updater not to run when disabled")
	}
}

func TestTriggerGeoLiteUpdate_DoesNotRunProxyGeoRefreshWhenUpdateFails(t *testing.T) {
	withTempRuntimeWorkingDir(t)

	originalCfg := config.GetConfig()
	originalUpdate := updateGeoLiteDatabases
	originalRefresh := runProxyGeoRefreshNow
	t.Cleanup(func() {
		updateGeoLiteDatabases = originalUpdate
		runProxyGeoRefreshNow = originalRefresh
		if err := config.SetConfig(originalCfg); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})

	cfg := originalCfg
	cfg.Plugins.GeoLite.Enabled = true
	cfg.Plugins.GeoLite.APIKey = "test-key"
	cfg.Plugins.GeoLite.AutoUpdate = true
	if err := config.SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}

	refreshCalled := false
	updateGeoLiteDatabases = func(context.Context) (bool, error) {
		return false, errors.New("download failed")
	}
	runProxyGeoRefreshNow = func(context.Context, string) {
		refreshCalled = true
	}

	triggerGeoLiteUpdate(context.Background(), "config-save", true)

	if refreshCalled {
		t.Fatal("expected proxy geo refresh not to run when GeoLite update fails")
	}
}

func TestRunProxyGeoRefresh_IgnoresFollowerInstance(t *testing.T) {
	originalRunner := runProxyGeoRefreshLeaderTaskOnce
	t.Cleanup(func() {
		runProxyGeoRefreshLeaderTaskOnce = originalRunner
	})

	called := false
	runProxyGeoRefreshLeaderTaskOnce = func(context.Context, string, time.Duration, func(context.Context) error) error {
		called = true
		return support.ErrLeaderLockNotAcquired
	}

	RunProxyGeoRefresh(context.Background(), "test")

	if !called {
		t.Fatal("expected leader task runner to be invoked")
	}
}

func withTempRuntimeWorkingDir(t *testing.T) {
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

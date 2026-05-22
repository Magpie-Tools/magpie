package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"magpie/internal/config"
	"magpie/internal/geolite"
	"magpie/internal/support"
)

const (
	geoLiteUpdateLockKey       = "magpie:leader:geolite_update"
	geoLiteUpdateFallbackEvery = 24 * time.Hour
)

var (
	updateGeoLiteDatabases = geolite.UpdateDatabases
	runProxyGeoRefreshNow  = RunProxyGeoRefresh
)

func StartGeoLiteUpdateRoutine(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	var intervalValue atomic.Value
	initialInterval := config.GetGeoLiteUpdateInterval()
	if initialInterval <= 0 {
		initialInterval = geoLiteUpdateFallbackEvery
	}
	intervalValue.Store(initialInterval)

	updateSignal := make(chan struct{}, 1)
	updates := config.GeoLiteUpdateIntervalUpdates()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case newInterval := <-updates:
				if newInterval <= 0 {
					newInterval = geoLiteUpdateFallbackEvery
				}
				intervalValue.Store(newInterval)
				select {
				case updateSignal <- struct{}{}:
				default:
				}
			}
		}
	}()

	err := support.RunWithLeader(ctx, geoLiteUpdateLockKey, support.DefaultLeadershipTTL, func(leaderCtx context.Context) {
		runGeoLiteUpdateLoop(leaderCtx, &intervalValue, updateSignal)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("GeoLite update routine stopped", "error", err)
	}
}

func runGeoLiteUpdateLoop(ctx context.Context, intervalValue *atomic.Value, updateSignal <-chan struct{}) {
	currentInterval := intervalValue.Load().(time.Duration)
	if currentInterval <= 0 {
		currentInterval = geoLiteUpdateFallbackEvery
	}

	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	triggerGeoLiteUpdate(ctx, "startup", true)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			triggerGeoLiteUpdate(ctx, "scheduled", false)
		case <-updateSignal:
			newInterval := intervalValue.Load().(time.Duration)
			if newInterval <= 0 {
				newInterval = geoLiteUpdateFallbackEvery
			}
			if newInterval == currentInterval {
				continue
			}
			drainTicker(ticker)
			currentInterval = newInterval
			ticker.Reset(currentInterval)
		}
	}
}

// RunGeoLiteUpdate runs the updater on demand. When force is false the update
// is only executed if auto updates are enabled.
func RunGeoLiteUpdate(ctx context.Context, reason string, force bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	triggerGeoLiteUpdate(ctx, reason, force)
}

func triggerGeoLiteUpdate(ctx context.Context, reason string, force bool) {
	cfg := config.GetConfig()
	if !cfg.Plugins.GeoLite.Enabled {
		log.Debug("GeoLite update skipped: plugin disabled", "reason", reason)
		return
	}

	apiKey := strings.TrimSpace(cfg.Plugins.GeoLite.APIKey)
	if apiKey == "" {
		log.Debug("GeoLite update skipped: API key missing", "reason", reason)
		return
	}

	if !force && !cfg.Plugins.GeoLite.AutoUpdate {
		log.Debug("GeoLite update skipped: auto update disabled", "reason", reason)
		return
	}

	updated, err := updateGeoLiteDatabases(ctx)
	switch {
	case errors.Is(err, geolite.ErrNoAPIKey):
		log.Debug("GeoLite update skipped: API key missing", "reason", reason)
	case err != nil:
		log.Error("GeoLite update failed", "reason", reason, "error", err)
	case updated:
		log.Info("GeoLite databases updated", "reason", reason)
		runProxyGeoRefreshNow(ctx, "geolite-"+reason)
	default:
		log.Debug("GeoLite update skipped", "reason", reason)
	}
}

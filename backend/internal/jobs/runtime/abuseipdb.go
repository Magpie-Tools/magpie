package runtime

import (
	"context"
	"errors"
	"strings"
	"time"

	"magpie/internal/abuseipdb"
	"magpie/internal/config"
	"magpie/internal/database"
	"magpie/internal/support"

	"github.com/charmbracelet/log"
)

const (
	abuseIPDBLockKey          = "magpie:leader:abuseipdb"
	abuseIPDBCheckTimeout     = 20 * time.Second
	abuseIPDBDisabledInterval = time.Minute
	abuseIPDBNoWorkInterval   = 15 * time.Minute
	abuseIPDBMinimumInterval  = time.Second
	abuseIPDBStatusPollLimit  = time.Hour
	abuseIPDBCheckStaleAfter  = 24 * time.Hour
)

func StartAbuseIPDBRoutine(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	err := support.RunWithLeader(ctx, abuseIPDBLockKey, support.DefaultLeadershipTTL, runAbuseIPDBLoop)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("AbuseIPDB routine stopped", "error", err)
	}
}

func runAbuseIPDBLoop(ctx context.Context) {
	client := abuseipdb.Client{}
	for {
		wait := runAbuseIPDBOnce(ctx, client)
		if wait <= 0 {
			wait = abuseIPDBMinimumInterval
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func runAbuseIPDBOnce(ctx context.Context, client abuseipdb.Client) time.Duration {
	cfg := config.GetConfig().Plugins.AbuseIPDB
	if !cfg.Enabled || strings.TrimSpace(cfg.APIKey) == "" {
		return abuseIPDBDisabledInterval
	}

	now := time.Now().UTC()
	if resetAt, ok := parseConfigTime(cfg.DailyResetAt); ok && cfg.DailyRemaining <= 0 && resetAt.After(now) {
		return minDuration(time.Until(resetAt), abuseIPDBStatusPollLimit)
	}

	staleBefore := now.Add(-abuseIPDBCheckStaleAfter)
	proxy, err := database.GetNextAbuseIPDBProxyToCheck(ctx, staleBefore)
	if err != nil {
		log.Error("AbuseIPDB: failed to select proxy", "error", err)
		return abuseIPDBNoWorkInterval
	}
	if proxy == nil {
		return abuseIPDBNoWorkInterval
	}

	checkCtx, cancel := context.WithTimeout(ctx, abuseIPDBCheckTimeout)
	result, rate, err := client.Check(checkCtx, cfg.APIKey, proxy.GetIp(), cfg.MaxAgeInDays)
	cancel()

	updateAbuseIPDBStatus(rate, err)
	if err != nil {
		log.Warn("AbuseIPDB check failed", "proxy_id", proxy.ID, "error", err)
		if rate.RetryAfter > 0 {
			return minDuration(rate.RetryAfter, abuseIPDBStatusPollLimit)
		}
		return abuseIPDBNoWorkInterval
	}

	checkedAt := time.Now().UTC()
	if err := database.UpsertAbuseIPDBCheck(context.Background(), proxy.ID, result, checkedAt); err != nil {
		log.Error("AbuseIPDB: failed to store check", "proxy_id", proxy.ID, "error", err)
		return abuseIPDBNoWorkInterval
	}
	if err := database.RecalculateProxyReputations(context.Background(), []uint64{proxy.ID}); err != nil {
		log.Error("AbuseIPDB: failed to refresh reputation", "proxy_id", proxy.ID, "error", err)
	}

	return nextAbuseIPDBDelay(config.GetConfig().Plugins.AbuseIPDB, time.Now().UTC())
}

func updateAbuseIPDBStatus(rate abuseipdb.RateLimit, checkErr error) {
	if rate.Limit == 0 && rate.Remaining == 0 && rate.ResetAt == nil && checkErr == nil {
		return
	}

	errText := ""
	if checkErr != nil {
		errText = checkErr.Error()
	}
	now := time.Now().UTC()

	if err := config.UpdateAbuseIPDBStatus(func(cfg *config.Config) {
		if rate.Limit > 0 {
			cfg.Plugins.AbuseIPDB.DailyLimit = rate.Limit
			cfg.Plugins.AbuseIPDB.DailyRemaining = rate.Remaining
		}
		if rate.ResetAt != nil {
			cfg.Plugins.AbuseIPDB.DailyResetAt = rate.ResetAt.UTC().Format(time.RFC3339)
		}
		cfg.Plugins.AbuseIPDB.LastCheckedAt = now.Format(time.RFC3339)
		cfg.Plugins.AbuseIPDB.LastError = errText
	}); err != nil {
		log.Warn("AbuseIPDB: failed to persist quota status", "error", err)
	}
}

func nextAbuseIPDBDelay(cfg config.AbuseIPDBPluginConfig, now time.Time) time.Duration {
	resetAt, ok := parseConfigTime(cfg.DailyResetAt)
	if !ok || !resetAt.After(now) || cfg.DailyRemaining <= 0 {
		return abuseIPDBNoWorkInterval
	}

	delay := resetAt.Sub(now) / time.Duration(cfg.DailyRemaining)
	if delay < abuseIPDBMinimumInterval {
		return abuseIPDBMinimumInterval
	}
	if delay > abuseIPDBStatusPollLimit {
		return abuseIPDBStatusPollLimit
	}
	return delay
}

func parseConfigTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

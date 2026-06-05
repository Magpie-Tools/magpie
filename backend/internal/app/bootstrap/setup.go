package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"magpie/internal/blacklist"
	"magpie/internal/config"
	"magpie/internal/database"
	"magpie/internal/domain"
	"magpie/internal/geolite"
	"magpie/internal/jobs/checker"
	"magpie/internal/jobs/checker/judges"
	"magpie/internal/jobs/maintenance"
	proxyqueue "magpie/internal/jobs/queue/proxy"
	sitequeue "magpie/internal/jobs/queue/sites"
	jobruntime "magpie/internal/jobs/runtime"
	"magpie/internal/jobs/scraper"
	"magpie/internal/rotatingproxy"
	"magpie/internal/support"
)

const startupProxyBatchSize = 2000
const startupQueueBootstrapLockKey = "magpie:leader:startup_queue_bootstrap"
const envStartupQueueBootstrapAsync = "STARTUP_QUEUE_BOOTSTRAP_ASYNC"
const startupQueueBootstrapRetryDelay = 10 * time.Second

var startupQueueBootstrapCompleted atomic.Bool
var startupQueueBootstrapRunning atomic.Bool

func Setup(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := config.ReadSettings(); err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	if redisClient, err := support.GetRedisClient(); err != nil {
		log.Warn("Redis synchronization disabled", "error", err)
	} else {
		config.EnableRedisSynchronization(ctx, redisClient)
		judges.EnableRedisSynchronization(ctx, redisClient)
		geolite.EnableRedisDistribution(ctx, redisClient)
	}

	if _, err := database.SetupDB(); err != nil {
		return fmt.Errorf("failed to set up database: %w", err)
	}
	config.SetBetweenTime()

	if err := blacklist.Initialize(ctx); err != nil {
		log.Warn("Blacklist initialisation failed", "error", err)
	}

	if redisClient, err := support.GetRedisClient(); err != nil {
		log.Warn("Blacklist synchronization disabled", "error", err)
	} else {
		blacklist.EnableRedisSynchronization(ctx, redisClient)
	}

	judgeSetup()

	cleanedRelations, orphanedProxies, cleanupErr := database.CleanupAutoRemovalViolations(ctx)
	if cleanupErr != nil {
		log.Error("auto-remove cleanup failed", "error", cleanupErr)
	} else if cleanedRelations > 0 {
		log.Info(
			"Auto-remove cleanup completed",
			"relations_removed", cleanedRelations,
			"orphaned_proxies", len(orphanedProxies),
		)
		if len(orphanedProxies) > 0 {
			if err := proxyqueue.PublicProxyQueue.RemoveFromQueue(orphanedProxies); err != nil {
				log.Warn("failed to purge orphaned proxies from queue", "error", err)
			}
		}
	}

	limitCleanedRelations, limitOrphanedProxies, limitCleanupErr := database.CleanupProxyLimitViolations(ctx)
	if limitCleanupErr != nil {
		log.Error("proxy-limit cleanup failed", "error", limitCleanupErr)
	} else if limitCleanedRelations > 0 {
		log.Info(
			"Proxy-limit cleanup completed",
			"relations_removed", limitCleanedRelations,
			"orphaned_proxies", len(limitOrphanedProxies),
		)
		if len(limitOrphanedProxies) > 0 {
			if err := proxyqueue.PublicProxyQueue.RemoveFromQueue(limitOrphanedProxies); err != nil {
				log.Warn("failed to purge proxy-limit orphans from queue", "error", err)
			}
		}
	}

	go func() {
		cfg := config.GetConfig()

		if config.GetCurrentIp() == "" && cfg.Checker.IpLookup == "" {
			return
		}

		for config.GetCurrentIp() == "" {
			select {
			case <-ctx.Done():
				return
			default:
			}

			html, err := checker.DefaultRequestWithContext(ctx, cfg.Checker.IpLookup)
			if err != nil {
				log.Error("Error checking IP address:", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
				continue
			}

			currentIp := support.FindIP(html)
			config.SetCurrentIp(currentIp)
			log.Infof("Found IP! Current IP: %s", currentIp)

			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}

	}()

	startupQueueBootstrapCompleted.Store(false)
	if support.GetEnvBool(envStartupQueueBootstrapAsync, true) {
		startBootstrapStartupQueuesAsync(ctx)
	} else {
		if err := bootstrapStartupQueues(ctx); err != nil {
			return err
		}
		startupQueueBootstrapCompleted.Store(true)
	}

	rotatingproxy.GlobalManager.StartAll()
	go func() {
		<-ctx.Done()
		rotatingproxy.GlobalManager.StopAll()
	}()
	syncIntervalSeconds := support.GetEnvInt("ROTATING_PROXY_SYNC_INTERVAL_SECONDS", 10)
	if syncIntervalSeconds <= 0 {
		syncIntervalSeconds = 10
	}
	go rotatingproxy.GlobalManager.StartSyncLoop(ctx, time.Duration(syncIntervalSeconds)*time.Second)

	// Routines

	go judges.StartJudgeRoutine(ctx)
	go jobruntime.StartProxyStatisticsRoutine(ctx)
	go jobruntime.StartProxyStatisticsRetentionRoutine(ctx)
	go jobruntime.StartProxyTimelineRetentionRoutine(ctx)
	go jobruntime.StartProxyHistoryRoutine(ctx)
	go jobruntime.StartProxySnapshotRoutine(ctx)
	go jobruntime.StartProxyGeoRefreshRoutine(ctx)
	go maintenance.StartOrphanCleanupRoutine(ctx)
	go maintenance.StartPasswordResetCleanupRoutine(ctx)
	go jobruntime.StartGeoLiteUpdateRoutine(ctx)
	go jobruntime.StartAbuseIPDBRoutine(ctx)
	go jobruntime.StartEmailDeliveryRoutine(ctx)
	go jobruntime.StartEmailDeliveryMaintenanceRoutine(ctx)
	go blacklist.StartRefreshRoutine(ctx)
	go checker.ThreadDispatcher(ctx)
	scraper.StartInfrastructure()
	go scraper.ThreadDispatcher(ctx)

	return nil
}

func StartupQueueBootstrapCompleted() bool {
	return startupQueueBootstrapCompleted.Load()
}

func judgeSetup() {
	addJudgeRelationsToCache()
	AddDefaultJudgesToUsers()
}

func startBootstrapStartupQueuesAsync(ctx context.Context) {
	if !startupQueueBootstrapRunning.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer startupQueueBootstrapRunning.Store(false)
		for {
			err := bootstrapStartupQueues(ctx)
			if err == nil {
				startupQueueBootstrapCompleted.Store(true)
				log.Info("Startup queue bootstrap completed")
				return
			}
			if errors.Is(err, context.Canceled) {
				return
			}

			log.Error("Startup queue bootstrap failed; retrying", "error", err, "retry_in", startupQueueBootstrapRetryDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(startupQueueBootstrapRetryDelay):
			}
		}
	}()
}

func bootstrapStartupQueues(ctx context.Context) error {
	err := support.RunLeaderTaskOnce(ctx, startupQueueBootstrapLockKey, support.DefaultLeadershipTTL, func(leaderCtx context.Context) error {
		if err := queueStartupProxies(leaderCtx); err != nil {
			return err
		}
		return queueStartupScrapeSites(leaderCtx)
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, support.ErrLeaderLockNotAcquired) {
		log.Info("Skipped startup queue bootstrap on follower instance")
		return nil
	}
	return err
}

func queueStartupProxies(ctx context.Context) error {
	queuedProxies := 0
	err := database.ForEachProxyBatch(startupProxyBatchSize, func(proxies []domain.Proxy) error {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		missingGeo := database.FilterProxiesMissingGeo(proxies)
		if len(missingGeo) > 0 {
			database.AsyncEnrichProxyMetadata(missingGeo)
		}

		if err := proxyqueue.PublicProxyQueue.AddToQueue(proxies); err != nil {
			return err
		}
		queuedProxies += len(proxies)
		return nil
	})
	if err != nil {
		return fmt.Errorf("queue startup proxies: %w", err)
	}
	if queuedProxies > 0 {
		log.Infof("Added %d proxies to queue", queuedProxies)
	}
	return nil
}

func queueStartupScrapeSites(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	scrapeSites, err := database.GetAllScrapeSites()
	if err != nil {
		return fmt.Errorf("load startup scrape sites: %w", err)
	}

	filtered := make([]domain.ScrapeSite, 0, len(scrapeSites))
	blocked := 0
	for _, site := range scrapeSites {
		if config.IsWebsiteBlocked(site.URL) {
			blocked++
			continue
		}
		filtered = append(filtered, site)
	}

	if blocked > 0 {
		log.Info("Skipped blocked scrape sites", "count", blocked)
	}

	if len(filtered) == 0 {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	if err := sitequeue.PublicScrapeSiteQueue.AddToQueue(filtered); err != nil {
		return fmt.Errorf("queue startup scrape sites: %w", err)
	}
	log.Infof("Added %d scrape sites to queue", len(filtered))
	return nil
}

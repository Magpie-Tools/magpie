package runtime

import (
	"context"
	"time"

	"magpie/internal/database"

	"github.com/charmbracelet/log"
)

const dashboardCacheRefreshInterval = 30 * time.Second

func StartDashboardCacheRoutine(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	ticker := time.NewTicker(dashboardCacheRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshDashboardCaches(ctx)
		}
	}
}

func refreshDashboardCaches(ctx context.Context) {
	start := time.Now()
	if err := database.RefreshDashboardCaches(ctx); err != nil {
		log.Warn("Dashboard cache refresh failed; serving the previous snapshot", "error", err)
		return
	}
	log.Debug("Dashboard caches refreshed", "duration", time.Since(start))
}

package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"magpie/internal/domain"

	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

const (
	proxyGeoRefreshBatchSize   = 2000
	proxyGeoRefreshUpdateChunk = 500
	proxyGeoRefreshWorkerLimit = 16
)

var (
	ErrProxyGeoRefreshDatabaseNotInitialized = errors.New("proxy geo refresh skipped: database not initialized")
	ErrProxyGeoRefreshGeoLiteUnavailable     = errors.New("proxy geo refresh skipped: GeoLite databases unavailable")
)

type proxyGeoUpdate struct {
	ID            uint64 `gorm:"primaryKey"`
	Country       string
	EstimatedType string
}

func (proxyGeoUpdate) TableName() string {
	return "proxies"
}

func RunProxyGeoRefresh(ctx context.Context, batchSize int) (int64, int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if DB == nil {
		return 0, 0, ErrProxyGeoRefreshDatabaseNotInitialized
	}

	if !GeoLiteAvailable() {
		return 0, 0, ErrProxyGeoRefreshGeoLiteUnavailable
	}

	return refreshProxyGeoData(ctx, batchSize)
}

func refreshProxyGeoData(ctx context.Context, batchSize int) (int64, int64, error) {
	if DB == nil {
		return 0, 0, ErrProxyGeoRefreshDatabaseNotInitialized
	}

	if batchSize <= 0 {
		batchSize = proxyGeoRefreshBatchSize
	}

	var (
		scanned int64
		updated int64
	)

	proxies := make([]domain.Proxy, 0, batchSize)

	result := DB.WithContext(ctx).
		Model(&domain.Proxy{}).
		Select("id", "ip", "country", "estimated_type").
		FindInBatches(&proxies, batchSize, func(tx *gorm.DB, batch int) error {
			if len(proxies) == 0 {
				return nil
			}

			currentBatch := make([]domain.Proxy, len(proxies))
			copy(currentBatch, proxies)

			updates, err := buildProxyGeoUpdates(ctx, currentBatch)
			scanned += int64(len(currentBatch))
			if err != nil {
				return err
			}
			if len(updates) == 0 {
				return nil
			}
			if err := applyProxyGeoUpdates(ctx, updates); err != nil {
				return err
			}
			updated += int64(len(updates))
			return nil
		})

	if result.Error != nil {
		return scanned, updated, result.Error
	}

	return scanned, updated, nil
}

func buildProxyGeoUpdates(ctx context.Context, proxies []domain.Proxy) ([]proxyGeoUpdate, error) {
	if len(proxies) == 0 {
		return nil, nil
	}

	updates := make([]proxyGeoUpdate, 0, len(proxies))
	var mu sync.Mutex

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(proxyGeoRefreshWorkerLimit)

	for _, proxy := range proxies {
		proxy := proxy
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}

			ip := proxy.GetIp()
			country := GetCountryCode(ip)
			if country == "" {
				country = "N/A"
			}

			proxyType := DetermineProxyType(ip)
			if proxyType == "" {
				proxyType = "N/A"
			}

			if country == proxy.Country && proxyType == proxy.EstimatedType {
				return nil
			}

			mu.Lock()
			updates = append(updates, proxyGeoUpdate{
				ID:            proxy.ID,
				Country:       country,
				EstimatedType: proxyType,
			})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return updates, nil
}

func applyProxyGeoUpdates(ctx context.Context, updates []proxyGeoUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	for start := 0; start < len(updates); start += proxyGeoRefreshUpdateChunk {
		end := start + proxyGeoRefreshUpdateChunk
		if end > len(updates) {
			end = len(updates)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		batch := updates[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, 0, len(batch)*3)

		for i, u := range batch {
			placeholders[i] = "(?::bigint, ?::text, ?::text)"
			args = append(args, u.ID, u.Country, u.EstimatedType)
		}

		query := fmt.Sprintf(
			`UPDATE proxies AS p SET country = data.country, estimated_type = data.estimated_type `+
				`FROM (VALUES %s) AS data(id, country, estimated_type) WHERE p.id = data.id`,
			strings.Join(placeholders, ","),
		)

		db := DB.WithContext(ctx)
		if err := db.Exec(query, args...).Error; err != nil {
			return err
		}
		proxyIDs := make([]uint64, 0, len(batch))
		for _, update := range batch {
			proxyIDs = append(proxyIDs, update.ID)
		}
		if err := refreshUserProxyFilterIndexesForProxyIDs(db, proxyIDs); err != nil {
			return err
		}
	}

	return nil
}

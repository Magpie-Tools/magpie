package database

import (
	"context"
	"fmt"

	"magpie/internal/domain"

	"gorm.io/gorm"
)

func DeleteUserAccount(ctx context.Context, userID uint) ([]domain.Proxy, []domain.ScrapeSite, error) {
	if DB == nil {
		return nil, nil, fmt.Errorf("database not initialised")
	}

	db := DB
	if ctx != nil {
		db = db.WithContext(ctx)
	}

	var orphanedProxies []domain.Proxy
	var orphanedScrapeSites []domain.ScrapeSite

	err := db.Transaction(func(tx *gorm.DB) error {
		var proxyIDs []int
		if err := tx.Table("user_proxies").
			Where("user_id = ?", userID).
			Distinct().
			Pluck("proxy_id", &proxyIDs).Error; err != nil {
			return err
		}

		var scrapeSiteIDs []int
		if err := tx.Table("user_scrape_site").
			Where("user_id = ?", userID).
			Distinct().
			Pluck("scrape_site_id", &scrapeSiteIDs).Error; err != nil {
			return err
		}

		if err := tx.Where("user_id = ?", userID).Delete(&domain.UserProxy{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&domain.UserScrapeSite{}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&domain.UserProxyFilterIndex{}) {
			if err := tx.Where("user_id = ?", userID).Delete(&domain.UserProxyFilterIndex{}).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&domain.UserScrapeSourceStat{}) {
			if err := tx.Where("user_id = ?", userID).Delete(&domain.UserScrapeSourceStat{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", userID).Delete(&domain.UserJudge{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&domain.RotatingProxy{}).Error; err != nil {
			return err
		}

		if err := tx.Delete(&domain.User{}, userID).Error; err != nil {
			return err
		}

		var err error
		orphanedProxies, err = loadOrphanedProxies(tx, proxyIDs)
		if err != nil {
			return err
		}

		orphanedScrapeSites, err = loadOrphanedScrapeSites(tx, scrapeSiteIDs)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return orphanedProxies, orphanedScrapeSites, nil
}

func loadOrphanedProxies(tx *gorm.DB, proxyIDs []int) ([]domain.Proxy, error) {
	if len(proxyIDs) == 0 {
		return nil, nil
	}

	chunkSize := deleteChunkSize
	if chunkSize <= 0 || chunkSize > len(proxyIDs) {
		chunkSize = len(proxyIDs)
	}

	orphans := make([]domain.Proxy, 0)
	for start := 0; start < len(proxyIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(proxyIDs) {
			end = len(proxyIDs)
		}

		var batch []domain.Proxy
		if err := tx.
			Where("id IN ?", proxyIDs[start:end]).
			Where("NOT EXISTS (SELECT 1 FROM user_proxies up WHERE up.proxy_id = proxies.id)").
			Find(&batch).Error; err != nil {
			return nil, err
		}

		if len(batch) > 0 {
			orphans = append(orphans, batch...)
		}
	}

	return orphans, nil
}

func loadOrphanedScrapeSites(tx *gorm.DB, scrapeSiteIDs []int) ([]domain.ScrapeSite, error) {
	if len(scrapeSiteIDs) == 0 {
		return nil, nil
	}

	chunkSize := deleteChunkSize
	if chunkSize <= 0 || chunkSize > len(scrapeSiteIDs) {
		chunkSize = len(scrapeSiteIDs)
	}

	orphans := make([]domain.ScrapeSite, 0)
	for start := 0; start < len(scrapeSiteIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(scrapeSiteIDs) {
			end = len(scrapeSiteIDs)
		}

		var batch []domain.ScrapeSite
		if err := tx.
			Where("id IN ?", scrapeSiteIDs[start:end]).
			Where("NOT EXISTS (SELECT 1 FROM user_scrape_site us WHERE us.scrape_site_id = scrape_sites.id)").
			Find(&batch).Error; err != nil {
			return nil, err
		}

		if len(batch) > 0 {
			orphans = append(orphans, batch...)
		}
	}

	return orphans, nil
}

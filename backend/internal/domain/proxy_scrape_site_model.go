package domain

import "time"

type ProxyScrapeSite struct {
	ProxyID      uint64    `gorm:"primaryKey;index:idx_proxy_scrape_site_scrape_proxy,priority:2"`
	ScrapeSiteID uint64    `gorm:"primaryKey;index:idx_proxy_scrape_site_scrape_proxy,priority:1"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`
}

func (ProxyScrapeSite) TableName() string {
	return "proxy_scrape_site"
}

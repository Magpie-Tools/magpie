package domain

import "time"

// UserScrapeSourceStat is a denormalized read model for scrape-source lists.
type UserScrapeSourceStat struct {
	UserID       uint   `gorm:"primaryKey;index:idx_user_scrape_source_stats_user_added,priority:1;index:idx_user_scrape_source_stats_user_protocol,priority:1"`
	ScrapeSiteID uint64 `gorm:"primaryKey"`

	URL         string `gorm:"not null"`
	ProtocolKey string `gorm:"size:16;not null;default:'';index:idx_user_scrape_source_stats_user_protocol,priority:2"`

	ProxyCount   uint `gorm:"not null;default:0;index"`
	AliveCount   uint `gorm:"not null;default:0;index"`
	DeadCount    uint `gorm:"not null;default:0"`
	UnknownCount uint `gorm:"not null;default:0"`

	AddedAt   time.Time `gorm:"index:idx_user_scrape_source_stats_user_added,priority:2,sort:desc"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (UserScrapeSourceStat) TableName() string {
	return "user_scrape_source_stats"
}

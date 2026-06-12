package domain

import "time"

// UserProxyFilterIndex is a denormalized read model for proxy list filtering.
type UserProxyFilterIndex struct {
	UserID  uint   `gorm:"primaryKey;index:idx_user_proxy_filter_user_alive_latest,priority:1;index:idx_user_proxy_filter_user_country,priority:1;index:idx_user_proxy_filter_user_type,priority:1;index:idx_user_proxy_filter_user_reputation,priority:1"`
	ProxyID uint64 `gorm:"primaryKey;index"`

	IPEncrypted string `gorm:"column:ip;default:''"`
	IPInt       uint32 `gorm:"index"`
	Port        uint16 `gorm:"not null;index"`

	Country       string `gorm:"size:56;not null;default:'N/A'"`
	CountryKey    string `gorm:"size:56;not null;default:'n/a';index:idx_user_proxy_filter_user_country,priority:2"`
	EstimatedType string `gorm:"size:20;not null;default:'N/A'"`
	TypeKey       string `gorm:"size:20;not null;default:'n/a';index:idx_user_proxy_filter_user_type,priority:2"`

	AnonymityLevel string `gorm:"size:50;not null;default:'N/A'"`
	AnonymityKey   string `gorm:"size:50;not null;default:'n/a';index"`

	Alive        bool      `gorm:"not null;default:false;index:idx_user_proxy_filter_user_alive_latest,priority:2"`
	LatestCheck  time.Time `gorm:"index:idx_user_proxy_filter_user_alive_latest,priority:3,sort:desc"`
	ResponseTime uint16    `gorm:"not null;default:0;index"`
	Attempt      uint8     `gorm:"not null;default:0"`

	HealthOverall *float32
	HealthHTTP    *float32
	HealthHTTPS   *float32
	HealthSOCKS4  *float32
	HealthSOCKS5  *float32

	AliveHTTP   bool `gorm:"not null;default:false;index"`
	AliveHTTPS  bool `gorm:"not null;default:false;index"`
	AliveSOCKS4 bool `gorm:"not null;default:false;index"`
	AliveSOCKS5 bool `gorm:"not null;default:false;index"`

	ReputationLabel string   `gorm:"size:16;not null;default:'unknown';index:idx_user_proxy_filter_user_reputation,priority:2"`
	ReputationScore *float32 `gorm:"index:idx_user_proxy_filter_user_reputation,priority:3,sort:desc"`

	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (UserProxyFilterIndex) TableName() string {
	return "user_proxy_filter_indexes"
}

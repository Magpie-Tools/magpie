package domain

import "time"

type AbuseIPDBCheck struct {
	ID                   uint64 `gorm:"primaryKey;autoIncrement"`
	ProxyID              uint64 `gorm:"not null;uniqueIndex"`
	AbuseConfidenceScore int    `gorm:"not null"`
	TotalReports         int    `gorm:"not null;default:0"`
	NumDistinctUsers     int    `gorm:"not null;default:0"`
	UsageType            string `gorm:"size:128;not null;default:''"`
	ISP                  string `gorm:"size:255;not null;default:''"`
	Domain               string `gorm:"size:255;not null;default:''"`
	CountryCode          string `gorm:"size:8;not null;default:''"`
	IsWhitelisted        *bool
	IsTor                bool       `gorm:"not null;default:false"`
	LastReportedAt       *time.Time `gorm:"index"`
	CheckedAt            time.Time  `gorm:"not null;index"`
	CreatedAt            time.Time  `gorm:"autoCreateTime"`
	UpdatedAt            time.Time  `gorm:"autoUpdateTime"`

	Proxy Proxy `gorm:"foreignKey:ProxyID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

func (AbuseIPDBCheck) TableName() string {
	return "abuseipdb_checks"
}

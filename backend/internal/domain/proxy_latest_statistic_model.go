package domain

import "time"

type ProxyLatestStatistic struct {
	ProxyID      uint64 `gorm:"primaryKey"`
	ProtocolID   int    `gorm:"primaryKey;index:idx_latest_protocol_alive,priority:1"`
	Alive        bool   `gorm:"not null;index:idx_latest_protocol_alive,priority:2"`
	StatisticID  uint64 `gorm:"not null"`
	ResponseTime uint16
	Attempt      uint8
	LevelID      *int
	JudgeID      uint
	CheckedAt    time.Time `gorm:"not null;index"`

	// Relationships
	Proxy    Proxy    `gorm:"foreignKey:ProxyID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Protocol Protocol `gorm:"foreignKey:ProtocolID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

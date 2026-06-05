package dto

import "time"

type ProxyFastestAlive struct {
	ID              uint64
	IP              string
	Port            uint16
	ResponseTime    uint16
	Country         string
	ReputationLabel string
	ReputationScore float32
	LatestCheck     time.Time
}

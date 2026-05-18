package domain

import (
	"magpie/internal/api/dto"
	"time"
)

var defaultProxyListColumns = []string{
	"alive",
	"ip_port",
	"response_time",
	"estimated_type",
	"country",
	"reputation",
	"latest_check",
	"actions",
}

var defaultScrapeSourceProxyColumns = []string{
	"alive",
	"ip_port",
	"response_time",
	"estimated_type",
	"country",
	"reputation",
	"latest_check",
	"actions",
}

var defaultScrapeSourceListColumns = []string{
	"url",
	"proxy_count",
	"health",
	"robots_check",
	"actions",
}

var validProxyListColumns = map[string]struct{}{
	"alive":          {},
	"health_overall": {},
	"health_http":    {},
	"health_https":   {},
	"health_socks4":  {},
	"health_socks5":  {},
	"ip":             {},
	"ip_port":        {},
	"port":           {},
	"response_time":  {},
	"estimated_type": {},
	"country":        {},
	"reputation":     {},
	"latest_check":   {},
	"check_now":      {},
	"actions":        {},
}

var validScrapeSourceListColumns = map[string]struct{}{
	"url":          {},
	"proxy_count":  {},
	"alive_count":  {},
	"health":       {},
	"robots_check": {},
	"scrape_now":   {},
	"actions":      {},
}

type User struct {
	ID       uint   `gorm:"primaryKey;autoIncrement"`
	Email    string `gorm:"not null;size:255"`
	Password string `gorm:"not null;size:100" json:"-"`
	Role     string `gorm:"not null;default:'user';check:role IN ('user', 'admin')"`

	//Settings
	HTTPProtocol               bool       `gorm:"not null;default:false"`
	HTTPSProtocol              bool       `gorm:"not null;default:true"`
	SOCKS4Protocol             bool       `gorm:"not null;default:false"`
	SOCKS5Protocol             bool       `gorm:"not null;default:false"`
	Timeout                    uint16     `gorm:"not null;default:7500"`
	Retries                    uint8      `gorm:"not null;default:2"`
	UseHttpsForSocks           bool       `gorm:"not null;default:true"`
	TransportProtocol          string     `gorm:"not null;default:'tcp'"`
	AutoRemoveFailingProxies   bool       `gorm:"not null;default:false"`
	AutoRemoveFailureThreshold uint8      `gorm:"not null;default:3"`
	ProxyListColumns           StringList `gorm:"type:jsonb;default:'[]'"`
	ScrapeSourceProxyColumns   StringList `gorm:"type:jsonb;default:'[]'"`
	ScrapeSourceListColumns    StringList `gorm:"type:jsonb;default:'[]'"`

	//Relations
	Judges       []Judge        `gorm:"many2many:user_judges;"`
	Proxies      []Proxy        `gorm:"many2many:user_proxies;"`
	ProxyHistory []ProxyHistory `gorm:"foreignKey:UserID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ScrapeSites  []ScrapeSite   `gorm:"many2many:user_scrape_site;"`
	CreatedAt    time.Time      `gorm:"autoCreateTime"`
}

func (u *User) ToUserSettings(simpleUserJudges []dto.SimpleUserJudge, scrapingSources []string) dto.UserSettings {
	return dto.UserSettings{
		HTTPProtocol:               u.HTTPProtocol,
		HTTPSProtocol:              u.HTTPSProtocol,
		SOCKS4Protocol:             u.SOCKS4Protocol,
		SOCKS5Protocol:             u.SOCKS5Protocol,
		Timeout:                    u.Timeout,
		Retries:                    u.Retries,
		UseHttpsForSocks:           u.UseHttpsForSocks,
		TransportProtocol:          u.TransportProtocol,
		AutoRemoveFailingProxies:   u.AutoRemoveFailingProxies,
		AutoRemoveFailureThreshold: u.AutoRemoveFailureThreshold,
		SimpleUserJudges:           simpleUserJudges,
		ScrapingSources:            scrapingSources,
		ProxyListColumns:           NormalizeProxyListColumns(u.ProxyListColumns.Clone()),
		ScrapeSourceProxyColumns:   NormalizeScrapeSourceProxyColumns(u.ScrapeSourceProxyColumns.Clone()),
		ScrapeSourceListColumns:    NormalizeScrapeSourceListColumns(u.ScrapeSourceListColumns.Clone()),
	}
}

func NormalizeProxyListColumns(columns []string) []string {
	if len(columns) == 0 {
		return append([]string(nil), defaultProxyListColumns...)
	}

	seen := make(map[string]struct{}, len(columns))
	normalized := make([]string, 0, len(columns))
	for _, column := range columns {
		canonical := canonicalProxyListColumn(column)
		if _, exists := validProxyListColumns[canonical]; !exists {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}

	if len(normalized) == 0 {
		return append([]string(nil), defaultProxyListColumns...)
	}

	return normalized
}

func NormalizeScrapeSourceProxyColumns(columns []string) []string {
	if len(columns) == 0 {
		return append([]string(nil), defaultScrapeSourceProxyColumns...)
	}

	seen := make(map[string]struct{}, len(columns))
	normalized := make([]string, 0, len(columns))
	for _, column := range columns {
		canonical := canonicalProxyListColumn(column)
		if _, exists := validProxyListColumns[canonical]; !exists {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}

	if len(normalized) == 0 {
		return append([]string(nil), defaultScrapeSourceProxyColumns...)
	}

	return normalized
}

func NormalizeScrapeSourceListColumns(columns []string) []string {
	if len(columns) == 0 {
		return append([]string(nil), defaultScrapeSourceListColumns...)
	}

	seen := make(map[string]struct{}, len(columns))
	normalized := make([]string, 0, len(columns))
	for _, column := range columns {
		canonical := canonicalScrapeSourceListColumn(column)
		if _, exists := validScrapeSourceListColumns[canonical]; !exists {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}

	if len(normalized) == 0 {
		return append([]string(nil), defaultScrapeSourceListColumns...)
	}

	return normalized
}

func canonicalScrapeSourceListColumn(column string) string {
	if column == "details" {
		return "actions"
	}
	return column
}

func canonicalProxyListColumn(column string) string {
	switch column {
	case "alive_ratio_overall":
		return "health_overall"
	case "alive_ratio_http":
		return "health_http"
	case "alive_ratio_https":
		return "health_https"
	case "alive_ratio_socks4":
		return "health_socks4"
	case "alive_ratio_socks5":
		return "health_socks5"
	default:
		return column
	}
}

func (u *User) GetProtocolMap() map[string]int {
	protocols := make(map[string]int)

	if u.HTTPProtocol {
		protocols["http"] = 1
	}
	if u.HTTPSProtocol {
		protocols["https"] = 2
	}
	if u.SOCKS4Protocol {
		protocols["socks4"] = 3
	}
	if u.SOCKS5Protocol {
		protocols["socks5"] = 4
	}

	return protocols
}

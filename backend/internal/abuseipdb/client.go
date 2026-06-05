package abuseipdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const checkEndpoint = "https://api.abuseipdb.com/api/v2/check"

type CheckResult struct {
	IP                   string
	AbuseConfidenceScore int
	TotalReports         int
	NumDistinctUsers     int
	UsageType            string
	ISP                  string
	Domain               string
	CountryCode          string
	IsWhitelisted        *bool
	IsTor                bool
	LastReportedAt       *time.Time
}

type RateLimit struct {
	Limit      int
	Remaining  int
	ResetAt    *time.Time
	RetryAfter time.Duration
}

type Client struct {
	HTTPClient *http.Client
}

func (c Client) Check(ctx context.Context, apiKey, ip string, maxAgeInDays uint32) (CheckResult, RateLimit, error) {
	apiKey = strings.TrimSpace(apiKey)
	ip = strings.TrimSpace(ip)
	if apiKey == "" {
		return CheckResult{}, RateLimit{}, fmt.Errorf("abuseipdb api key is empty")
	}
	if ip == "" {
		return CheckResult{}, RateLimit{}, fmt.Errorf("ip address is empty")
	}
	if maxAgeInDays == 0 {
		maxAgeInDays = 30
	}
	if maxAgeInDays > 365 {
		maxAgeInDays = 365
	}

	endpoint, err := url.Parse(checkEndpoint)
	if err != nil {
		return CheckResult{}, RateLimit{}, err
	}
	query := endpoint.Query()
	query.Set("ipAddress", ip)
	query.Set("maxAgeInDays", strconv.FormatUint(uint64(maxAgeInDays), 10))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return CheckResult{}, RateLimit{}, err
	}
	req.Header.Set("Key", apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "magpie-abuseipdb/1.0")

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return CheckResult{}, RateLimit{}, err
	}
	defer resp.Body.Close()

	rate := ParseRateLimit(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Errors []struct {
				Detail string `json:"detail"`
				Status int    `json:"status"`
			} `json:"errors"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if len(apiErr.Errors) > 0 && strings.TrimSpace(apiErr.Errors[0].Detail) != "" {
			return CheckResult{}, rate, fmt.Errorf("abuseipdb check failed: %s", apiErr.Errors[0].Detail)
		}
		return CheckResult{}, rate, fmt.Errorf("abuseipdb check failed: HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			IPAddress            string `json:"ipAddress"`
			AbuseConfidenceScore int    `json:"abuseConfidenceScore"`
			CountryCode          string `json:"countryCode"`
			UsageType            string `json:"usageType"`
			ISP                  string `json:"isp"`
			Domain               string `json:"domain"`
			IsWhitelisted        *bool  `json:"isWhitelisted"`
			IsTor                bool   `json:"isTor"`
			TotalReports         int    `json:"totalReports"`
			NumDistinctUsers     int    `json:"numDistinctUsers"`
			LastReportedAt       string `json:"lastReportedAt"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return CheckResult{}, rate, fmt.Errorf("decode abuseipdb check response: %w", err)
	}

	var lastReportedAt *time.Time
	if parsed, ok := parseAPITime(payload.Data.LastReportedAt); ok {
		lastReportedAt = &parsed
	}

	return CheckResult{
		IP:                   payload.Data.IPAddress,
		AbuseConfidenceScore: clampScore(payload.Data.AbuseConfidenceScore),
		TotalReports:         payload.Data.TotalReports,
		NumDistinctUsers:     payload.Data.NumDistinctUsers,
		UsageType:            payload.Data.UsageType,
		ISP:                  payload.Data.ISP,
		Domain:               payload.Data.Domain,
		CountryCode:          payload.Data.CountryCode,
		IsWhitelisted:        payload.Data.IsWhitelisted,
		IsTor:                payload.Data.IsTor,
		LastReportedAt:       lastReportedAt,
	}, rate, nil
}

func ParseRateLimit(header http.Header) RateLimit {
	rate := RateLimit{
		Limit:     parseIntHeader(header.Get("X-RateLimit-Limit")),
		Remaining: parseIntHeader(header.Get("X-RateLimit-Remaining")),
	}

	if resetUnix := parseIntHeader(header.Get("X-RateLimit-Reset")); resetUnix > 0 {
		reset := time.Unix(int64(resetUnix), 0).UTC()
		rate.ResetAt = &reset
	}
	if retryAfter := parseIntHeader(header.Get("Retry-After")); retryAfter > 0 {
		rate.RetryAfter = time.Duration(retryAfter) * time.Second
	}

	return rate
}

func parseIntHeader(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func parseAPITime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func clampScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

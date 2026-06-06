package database

import (
	"testing"

	"magpie/internal/api/dto"
)

func TestGetDashboardInfoServesCachedSnapshotWithoutDatabase(t *testing.T) {
	const userID = uint(991)
	expected := dto.DashboardInfo{
		TotalChecks:      123,
		TotalChecksWeek:  45,
		TotalScraped:     67,
		TotalScrapedWeek: 8,
	}

	dashboardInfoCache.Store(userID, dashboardInfoCacheEntry{info: expected})
	t.Cleanup(func() {
		dashboardInfoCache.Delete(userID)
	})

	previousDB := DB
	DB = nil
	t.Cleanup(func() {
		DB = previousDB
	})

	actual := GetDashboardInfo(userID)
	if actual.TotalChecks != expected.TotalChecks ||
		actual.TotalChecksWeek != expected.TotalChecksWeek ||
		actual.TotalScraped != expected.TotalScraped ||
		actual.TotalScrapedWeek != expected.TotalScrapedWeek {
		t.Fatalf("GetDashboardInfo() = %+v, want cached %+v", actual, expected)
	}
}

func TestDashboardProxyListsServeCachedSnapshotsWithoutDatabase(t *testing.T) {
	const userID = uint(992)
	recentKey := dashboardProxyListCacheKey{UserID: userID, Limit: defaultRecentProxyChecksLimit}
	fastestKey := dashboardProxyListCacheKey{UserID: userID, Limit: dashboardFastestAliveLimit}
	expectedRecent := []dto.ProxyRecentCheck{{ID: 12, Port: 8080}}
	expectedFastest := []dto.ProxyFastestAlive{{ID: 34, Port: 1080}}

	dashboardRecentChecksCache.Store(recentKey, expectedRecent)
	dashboardFastestAliveCache.Store(fastestKey, expectedFastest)
	t.Cleanup(func() {
		dashboardRecentChecksCache.Delete(recentKey)
		dashboardFastestAliveCache.Delete(fastestKey)
	})

	previousDB := DB
	DB = nil
	t.Cleanup(func() {
		DB = previousDB
	})

	recent := GetRecentProxyChecks(userID, defaultRecentProxyChecksLimit)
	if len(recent) != 1 || recent[0].ID != expectedRecent[0].ID {
		t.Fatalf("GetRecentProxyChecks() = %+v, want cached %+v", recent, expectedRecent)
	}

	fastest := GetFastestAliveProxies(userID, dashboardFastestAliveLimit)
	if len(fastest) != 1 || fastest[0].ID != expectedFastest[0].ID {
		t.Fatalf("GetFastestAliveProxies() = %+v, want cached %+v", fastest, expectedFastest)
	}
}

func TestNormalizeDashboardCountry(t *testing.T) {
	tests := []struct {
		name    string
		country string
		want    string
	}{
		{name: "empty", country: "", want: "Unknown"},
		{name: "spaces", country: "   ", want: "Unknown"},
		{name: "n/a", country: "N/A", want: "Unknown"},
		{name: "unknown", country: "unknown", want: "Unknown"},
		{name: "unk", country: "UNK", want: "Unknown"},
		{name: "trim known country", country: "  Germany  ", want: "Germany"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDashboardCountry(tt.country); got != tt.want {
				t.Fatalf("normalizeDashboardCountry(%q) = %q, want %q", tt.country, got, tt.want)
			}
		})
	}
}

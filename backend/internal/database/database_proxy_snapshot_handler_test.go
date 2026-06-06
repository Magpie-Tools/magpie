package database

import (
	"testing"
	"time"

	"magpie/internal/domain"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNormalizeProxySnapshotLimit(t *testing.T) {
	if got := normalizeProxySnapshotLimit(0); got != defaultProxySnapshotLimit {
		t.Fatalf("normalizeProxySnapshotLimit(0) = %d, want %d", got, defaultProxySnapshotLimit)
	}

	if got := normalizeProxySnapshotLimit(-3); got != defaultProxySnapshotLimit {
		t.Fatalf("normalizeProxySnapshotLimit(-3) = %d, want %d", got, defaultProxySnapshotLimit)
	}

	if got := normalizeProxySnapshotLimit(144); got != 144 {
		t.Fatalf("normalizeProxySnapshotLimit(144) = %d, want 144", got)
	}

	if got := normalizeProxySnapshotLimit(maxProxySnapshotLimit + 500); got != maxProxySnapshotLimit {
		t.Fatalf("normalizeProxySnapshotLimit(too large) = %d, want %d", got, maxProxySnapshotLimit)
	}
}

func TestGetProxySnapshotCountSummaryUsesLatestAndSinceBaseline(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.User{}, &domain.ProxySnapshot{}); err != nil {
		t.Fatalf("migrate snapshot schema: %v", err)
	}

	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
	})

	now := time.Date(2026, time.June, 6, 12, 0, 0, 0, time.UTC)
	snapshots := []domain.ProxySnapshot{
		{UserID: 1, Metric: domain.ProxySnapshotMetricScraped, Count: 100, CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{UserID: 1, Metric: domain.ProxySnapshotMetricScraped, Count: 120, CreatedAt: now.Add(-6 * 24 * time.Hour)},
		{UserID: 1, Metric: domain.ProxySnapshotMetricScraped, Count: 150, CreatedAt: now},
	}
	if err := db.Create(&snapshots).Error; err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}

	summary := getProxySnapshotCountSummary(1, domain.ProxySnapshotMetricScraped, now.Add(-7*24*time.Hour))
	if !summary.Found {
		t.Fatal("expected snapshot summary")
	}
	if summary.Current != 150 {
		t.Fatalf("current count = %d, want 150", summary.Current)
	}
	if summary.Increase != 30 {
		t.Fatalf("count increase = %d, want 30", summary.Increase)
	}
}

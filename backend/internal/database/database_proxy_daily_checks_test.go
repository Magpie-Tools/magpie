package database

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDashboardCheckCountsFollowCurrentProxyOwnership(t *testing.T) {
	db := openDailyChecksSQLite(t)

	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
	})

	// Current ownership: proxy 1 belongs only to user 2.
	if err := db.Exec(`INSERT INTO user_proxies (user_id, proxy_id) VALUES (?, ?)`, 2, 1).Error; err != nil {
		t.Fatalf("insert user_proxies: %v", err)
	}

	oldDay := startOfUTCDay(time.Now().UTC().AddDate(0, 0, -10))
	if err := db.Exec(
		`INSERT INTO proxy_daily_checks (proxy_id, day, checks_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		1, oldDay, 3, time.Now().UTC(), time.Now().UTC(),
	).Error; err != nil {
		t.Fatalf("insert proxy_daily_checks: %v", err)
	}

	// Seed raw statistics to confirm daily and direct paths are aligned.
	for i := 0; i < 3; i++ {
		ts := oldDay.Add(time.Duration(i) * time.Hour)
		if err := db.Exec(`INSERT INTO proxy_statistics (proxy_id, created_at) VALUES (?, ?)`, 1, ts).Error; err != nil {
			t.Fatalf("insert proxy_statistics row %d: %v", i, err)
		}
	}

	currentDay := startOfUTCDay(time.Now().UTC())
	if err := db.Exec(
		`INSERT INTO proxy_daily_checks (proxy_id, day, checks_count, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		1, currentDay, 2, time.Now().UTC(), time.Now().UTC(),
	).Error; err != nil {
		t.Fatalf("insert current proxy_daily_checks: %v", err)
	}
	for i := 0; i < 2; i++ {
		ts := currentDay.Add(time.Duration(i+1) * time.Hour)
		if err := db.Exec(`INSERT INTO proxy_statistics (proxy_id, created_at) VALUES (?, ?)`, 1, ts).Error; err != nil {
			t.Fatalf("insert current proxy_statistics row %d: %v", i, err)
		}
	}

	weekAgo := time.Now().UTC().AddDate(0, 0, -7)

	user1Daily, err := queryDashboardCheckCountsFromDaily(1, weekAgo)
	if err != nil {
		t.Fatalf("queryDashboardCheckCountsFromDaily user1: %v", err)
	}
	user1Direct, err := loadDashboardCheckCountsDirect(1, weekAgo)
	if err != nil {
		t.Fatalf("loadDashboardCheckCountsDirect user1: %v", err)
	}
	if user1Daily.TotalChecks != 0 || user1Direct.TotalChecks != 0 {
		t.Fatalf("user1 totals = daily:%d direct:%d, want 0/0", user1Daily.TotalChecks, user1Direct.TotalChecks)
	}

	user2Daily, err := queryDashboardCheckCountsFromDaily(2, weekAgo)
	if err != nil {
		t.Fatalf("queryDashboardCheckCountsFromDaily user2: %v", err)
	}
	user2Direct, err := loadDashboardCheckCountsDirect(2, weekAgo)
	if err != nil {
		t.Fatalf("loadDashboardCheckCountsDirect user2: %v", err)
	}
	if user2Daily.TotalChecks != 5 || user2Direct.TotalChecks != 5 {
		t.Fatalf("user2 totals = daily:%d direct:%d, want 5/5", user2Daily.TotalChecks, user2Direct.TotalChecks)
	}
	if user2Daily.TotalChecksWeek != 2 || user2Direct.TotalChecksWeek != 2 {
		t.Fatalf("user2 weekly totals = daily:%d direct:%d, want 2/2", user2Daily.TotalChecksWeek, user2Direct.TotalChecksWeek)
	}
}

func openDailyChecksSQLite(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	stmts := []string{
		`CREATE TABLE user_proxies (
			user_id INTEGER NOT NULL,
			proxy_id INTEGER NOT NULL
		);`,
		`CREATE TABLE proxy_daily_checks (
			proxy_id INTEGER NOT NULL,
			day DATETIME NOT NULL,
			checks_count BIGINT NOT NULL DEFAULT 0,
			created_at DATETIME,
			updated_at DATETIME,
			PRIMARY KEY (proxy_id, day)
		);`,
		`CREATE TABLE proxy_statistics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			proxy_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL
		);`,
	}

	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("setup sqlite schema failed: %v", err)
		}
	}

	return db
}

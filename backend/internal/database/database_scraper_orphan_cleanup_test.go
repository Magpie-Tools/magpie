package database

import (
	"context"
	"testing"

	"magpie/internal/domain"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDeleteOrphanProxyScrapeSiteRelations(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.Proxy{}, &domain.ProxyScrapeSite{}); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
	})

	proxy := domain.Proxy{ID: 1, Port: 8080, Country: "N/A", EstimatedType: "N/A"}
	if err := db.Create(&proxy).Error; err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	links := []domain.ProxyScrapeSite{
		{ProxyID: 1, ScrapeSiteID: 10},
		{ProxyID: 2, ScrapeSiteID: 10},
		{ProxyID: 2, ScrapeSiteID: 11},
	}
	if err := db.Create(&links).Error; err != nil {
		t.Fatalf("create source links: %v", err)
	}

	removed, err := DeleteOrphanProxyScrapeSiteRelations(context.Background())
	if err != nil {
		t.Fatalf("delete orphan source links: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed links = %d, want 2", removed)
	}

	var remaining []domain.ProxyScrapeSite
	if err := db.Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining source links: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ProxyID != 1 {
		t.Fatalf("remaining links = %#v, want proxy 1 only", remaining)
	}
}

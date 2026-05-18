package domain

import "testing"

func TestNormalizeProxyListColumns_PreservesCheckNow(t *testing.T) {
	columns := NormalizeProxyListColumns([]string{"alive", "check_now", "actions"})

	if len(columns) != 3 {
		t.Fatalf("len(columns) = %d, want 3", len(columns))
	}
	if columns[1] != "check_now" {
		t.Fatalf("columns[1] = %q, want check_now", columns[1])
	}
}

func TestNormalizeScrapeSourceListColumns_PreservesScrapeNow(t *testing.T) {
	columns := NormalizeScrapeSourceListColumns([]string{"url", "scrape_now", "actions"})

	if len(columns) != 3 {
		t.Fatalf("len(columns) = %d, want 3", len(columns))
	}
	if columns[1] != "scrape_now" {
		t.Fatalf("columns[1] = %q, want scrape_now", columns[1])
	}
}

func TestNormalizeScrapeSourceListColumns_PreservesAliveCount(t *testing.T) {
	columns := NormalizeScrapeSourceListColumns([]string{"url", "alive_count", "actions"})

	if len(columns) != 3 {
		t.Fatalf("len(columns) = %d, want 3", len(columns))
	}
	if columns[1] != "alive_count" {
		t.Fatalf("columns[1] = %q, want alive_count", columns[1])
	}
}

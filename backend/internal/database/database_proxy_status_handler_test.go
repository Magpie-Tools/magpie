package database

import (
	"testing"
	"time"

	"magpie/internal/domain"
)

func TestLatestProxyStatusEntriesCopiesListSummaryFields(t *testing.T) {
	checkedAt := time.Date(2026, time.June, 6, 12, 30, 0, 0, time.UTC)
	levelID := 3
	judgeID := uint(9)

	entries, proxyIDs := latestProxyStatusEntries([]domain.ProxyStatistic{
		{
			ID:           42,
			ProxyID:      7,
			ProtocolID:   2,
			Alive:        true,
			ResponseTime: 315,
			Attempt:      4,
			LevelID:      &levelID,
			JudgeID:      judgeID,
			CreatedAt:    checkedAt,
		},
	})

	if len(entries) != 1 {
		t.Fatalf("latest entries = %d, want 1", len(entries))
	}
	if len(proxyIDs) != 1 || proxyIDs[0] != 7 {
		t.Fatalf("proxy IDs = %v, want [7]", proxyIDs)
	}

	entry := entries[0]
	if entry.StatisticID != 42 {
		t.Fatalf("statistic ID = %d, want 42", entry.StatisticID)
	}
	if entry.ResponseTime != 315 {
		t.Fatalf("response time = %d, want 315", entry.ResponseTime)
	}
	if entry.Attempt != 4 {
		t.Fatalf("attempt = %d, want 4", entry.Attempt)
	}
	if entry.LevelID == nil || *entry.LevelID != levelID {
		t.Fatalf("level ID = %v, want %d", entry.LevelID, levelID)
	}
	if entry.JudgeID != judgeID {
		t.Fatalf("judge ID = %d, want %d", entry.JudgeID, judgeID)
	}
	if !entry.CheckedAt.Equal(checkedAt) {
		t.Fatalf("checked at = %v, want %v", entry.CheckedAt, checkedAt)
	}
}

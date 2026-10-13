package domain

import (
	"testing"
	"time"
)

func TestCommunityStreakMilestonesAndBangkokDay(t *testing.T) {
	now := time.Date(2026, 10, 13, 17, 1, 0, 0, time.UTC)
	if CommunityDay(now) != "2026-10-14" {
		t.Fatal("Bangkok date boundary lost")
	}
	stats := BuildCommunityStats(3, 5, 3, 5, []string{"2026-10-10", "2026-10-11", "2026-10-12", "2026-10-13", "2026-10-14", "2026-10-14", "2099-01-01", "invalid"}, now)
	if stats.Points != 50 || stats.CurrentStreak != 5 || stats.BestStreak != 5 || len(stats.Achievements) != 16 {
		t.Fatalf("invalid stats %+v", stats)
	}
	found := map[string]bool{}
	for _, a := range stats.Achievements {
		found[a.ID] = a.Unlocked
	}
	for _, id := range []string{"resolve-1", "resolve-3", "resolve-5", "streak-5", "knowledge-3"} {
		if !found[id] {
			t.Fatalf("missing badge %s", id)
		}
	}
	if found["resolve-10"] || found["streak-10"] {
		t.Fatal("unearned badge unlocked")
	}
	expired := BuildCommunityStats(0, 0, 0, 0, []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05"}, now)
	if expired.CurrentStreak != 0 || expired.BestStreak != 5 {
		t.Fatal("best streak should survive a break")
	}
}

package uct

import (
	"math"
	"testing"
	"time"
)

func TestSelectPrefersUnvisitedArm(t *testing.T) {
	arms := []Arm{
		{Key: "direct", Visits: 4, TotalReward: 3.0},
		{Key: "cambrian", Visits: 0, TotalReward: 0},
		{Key: "turtle", Visits: 6, TotalReward: 5.0},
	}

	selected, ok := Select(arms, 1.2)
	if !ok {
		t.Fatal("expected selection")
	}
	if selected.Key != "cambrian" {
		t.Fatalf("selected key = %q, want cambrian", selected.Key)
	}
}

func TestSelectUsesRewardWhenVisited(t *testing.T) {
	arms := []Arm{
		{Key: "direct", Visits: 10, TotalReward: 4.0},
		{Key: "turtle", Visits: 10, TotalReward: 8.0},
	}

	selected, ok := Select(arms, 0)
	if !ok {
		t.Fatal("expected selection")
	}
	if selected.Key != "turtle" {
		t.Fatalf("selected key = %q, want turtle", selected.Key)
	}
}

func TestSelectTieBreaksByPriorThenKey(t *testing.T) {
	arms := []Arm{
		{Key: "turtle", Visits: 0, Prior: 0.5},
		{Key: "direct", Visits: 0, Prior: 0.7},
		{Key: "cambrian", Visits: 0, Prior: 0.7},
	}

	selected, ok := Select(arms, 1.0)
	if !ok {
		t.Fatal("expected selection")
	}
	if selected.Key != "cambrian" {
		t.Fatalf("selected key = %q, want cambrian", selected.Key)
	}
}

func TestDecayByAgeHalfLife(t *testing.T) {
	got := DecayByAge(10, 2*time.Hour, 2*time.Hour)
	if math.Abs(got-5) > 0.0001 {
		t.Fatalf("decayed value = %f, want 5", got)
	}
}

func TestPruneStaleArms(t *testing.T) {
	now := time.Now().UTC()
	arms := []Arm{
		{Key: "unvisited", Visits: 0},
		{Key: "fresh-low", Visits: 1, LastSeen: now.Add(-2 * time.Hour)},
		{Key: "stale-low", Visits: 1, LastSeen: now.Add(-72 * time.Hour)},
		{Key: "stale-high", Visits: 5, LastSeen: now.Add(-72 * time.Hour)},
	}

	got := PruneStaleArms(arms, now, 3, 24*time.Hour)
	if len(got) != 3 {
		t.Fatalf("pruned len = %d, want 3", len(got))
	}
	found := map[string]bool{}
	for _, arm := range got {
		found[arm.Key] = true
	}
	if found["stale-low"] {
		t.Fatal("expected stale-low to be pruned")
	}
	if !found["unvisited"] || !found["fresh-low"] || !found["stale-high"] {
		t.Fatalf("unexpected pruning result: %#v", found)
	}
}

func TestSelectEmptyArms(t *testing.T) {
	_, ok := Select(nil, 1.0)
	if ok {
		t.Fatal("expected no selection from empty arms")
	}
}

func TestSelectNegativeExploration(t *testing.T) {
	arms := []Arm{
		{Key: "a", Visits: 10, TotalReward: 5.0},
		{Key: "b", Visits: 10, TotalReward: 8.0},
	}
	selected, ok := Select(arms, -1.0)
	if !ok {
		t.Fatal("expected selection")
	}
	// Negative exploration clamped to 0, so pure exploit — b wins.
	if selected.Key != "b" {
		t.Fatalf("selected key = %q, want b", selected.Key)
	}
}

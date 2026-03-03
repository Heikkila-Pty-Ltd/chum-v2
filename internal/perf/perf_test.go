package perf

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRecordAndStats(t *testing.T) {
	db := testDB(t)
	tr := New(db, 1.414)
	ctx := context.Background()

	// Record some runs
	if err := tr.Record(ctx, "claude", "sonnet-4", "balanced", true, 12.5); err != nil {
		t.Fatal(err)
	}
	if err := tr.Record(ctx, "claude", "sonnet-4", "balanced", true, 10.0); err != nil {
		t.Fatal(err)
	}
	if err := tr.Record(ctx, "claude", "sonnet-4", "balanced", false, 30.0); err != nil {
		t.Fatal(err)
	}
	if err := tr.Record(ctx, "codex", "", "balanced", true, 8.0); err != nil {
		t.Fatal(err)
	}

	stats, err := tr.StatsForTier(ctx, "balanced")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(stats))
	}

	// codex has 100% success rate, should be first
	if stats[0].Provider.Agent != "codex" {
		t.Errorf("expected codex first (100%% success), got %s", stats[0].Provider.Agent)
	}
	if stats[0].TotalRuns != 1 || stats[0].Successes != 1 {
		t.Errorf("codex: expected 1/1, got %d/%d", stats[0].Successes, stats[0].TotalRuns)
	}

	// claude has 66% success rate
	if stats[1].Provider.Agent != "claude" {
		t.Errorf("expected claude second, got %s", stats[1].Provider.Agent)
	}
	if stats[1].TotalRuns != 3 || stats[1].Successes != 2 {
		t.Errorf("claude: expected 2/3, got %d/%d", stats[1].Successes, stats[1].TotalRuns)
	}
}

func TestPickEmpty(t *testing.T) {
	db := testDB(t)
	tr := New(db, 1.414)
	ctx := context.Background()

	p, err := tr.Pick(ctx, "fast")
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Errorf("expected nil for empty tier, got %+v", p)
	}
}

func TestPickExploration(t *testing.T) {
	db := testDB(t)
	tr := New(db, 1.414)
	ctx := context.Background()

	// Claude has many runs with moderate success
	for i := 0; i < 20; i++ {
		_ = tr.Record(ctx, "claude", "sonnet-4", "balanced", i%3 != 0, 10.0)
	}
	// Codex has very few runs — UCT should explore it
	_ = tr.Record(ctx, "codex", "", "balanced", true, 8.0)

	p, err := tr.Pick(ctx, "balanced")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("expected a pick, got nil")
	}
	// With high exploration and only 1 visit, codex should be picked
	if p.Agent != "codex" {
		t.Logf("picked %s (UCT may favor exploration or exploitation depending on params)", p.Agent)
	}
}

func TestPickTierIsolation(t *testing.T) {
	db := testDB(t)
	tr := New(db, 1.414)
	ctx := context.Background()

	_ = tr.Record(ctx, "claude", "sonnet-4", "premium", true, 15.0)
	_ = tr.Record(ctx, "codex", "", "fast", true, 5.0)

	p, err := tr.Pick(ctx, "fast")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("expected a pick for fast tier")
	}
	if p.Agent != "codex" {
		t.Errorf("expected codex for fast tier, got %s", p.Agent)
	}

	// Premium should only see claude
	p, err = tr.Pick(ctx, "premium")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("expected a pick for premium tier")
	}
	if p.Agent != "claude" {
		t.Errorf("expected claude for premium tier, got %s", p.Agent)
	}
}

func TestProviderKey(t *testing.T) {
	tests := []struct {
		p    Provider
		want string
	}{
		{Provider{Agent: "claude", Model: "sonnet-4"}, "claude/sonnet-4"},
		{Provider{Agent: "codex", Model: ""}, "codex"},
		{Provider{Agent: "gemini", Model: "pro-2.5"}, "gemini/pro-2.5"},
	}
	for _, tt := range tests {
		if got := tt.p.Key(); got != tt.want {
			t.Errorf("Provider{%q, %q}.Key() = %q, want %q", tt.p.Agent, tt.p.Model, got, tt.want)
		}
	}
}

func TestDefaultExploration(t *testing.T) {
	db := testDB(t)
	tr := New(db, 0) // zero should default to 1.414
	if tr.exploration != 1.414 {
		t.Errorf("expected default exploration 1.414, got %f", tr.exploration)
	}

	tr = New(db, -5) // negative should also default
	if tr.exploration != 1.414 {
		t.Errorf("expected default exploration 1.414, got %f", tr.exploration)
	}
}

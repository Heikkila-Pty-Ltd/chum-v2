package genome

import (
	"context"
	"database/sql"
	"math"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setupEngine(t *testing.T) (*Engine, *SQLiteStore) {
	t.Helper()
	db := setupTestDB(t)
	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(store), store
}

func TestRegisterAndListSpecies(t *testing.T) {
	eng, store := setupEngine(t)
	ctx := context.Background()

	err := eng.RegisterSpecies(ctx, Species{
		Name:  "claude-sonnet-fast",
		Agent: "claude",
		Model: "sonnet-4",
		Tier:  "fast",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = eng.RegisterSpecies(ctx, Species{
		Name:  "codex-gpt4-balanced",
		Agent: "codex",
		Model: "gpt-4.1",
		Tier:  "balanced",
	})
	if err != nil {
		t.Fatal(err)
	}

	species, err := store.ListSpecies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(species) != 2 {
		t.Fatalf("expected 2 species, got %d", len(species))
	}
}

func TestRecordOutcomeAndFitness(t *testing.T) {
	eng, store := setupEngine(t)
	ctx := context.Background()

	_ = eng.RegisterSpecies(ctx, Species{
		ID: "test-species", Name: "test", Agent: "claude", Model: "sonnet-4", Tier: "fast",
	})

	// Record 7 successes, 3 failures
	for i := 0; i < 7; i++ {
		if err := eng.RecordOutcome(ctx, "test-species", true, 0.8, 30.0, 5000); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := eng.RecordOutcome(ctx, "test-species", false, -0.5, 60.0, 8000); err != nil {
			t.Fatal(err)
		}
	}

	fitness, err := store.GetFitness(ctx, "test-species")
	if err != nil {
		t.Fatal(err)
	}

	if fitness.TotalRuns != 10 {
		t.Errorf("expected 10 runs, got %d", fitness.TotalRuns)
	}
	if fitness.Successes != 7 {
		t.Errorf("expected 7 successes, got %d", fitness.Successes)
	}
	if fitness.Failures != 3 {
		t.Errorf("expected 3 failures, got %d", fitness.Failures)
	}
	if math.Abs(fitness.SuccessRate-0.7) > 0.01 {
		t.Errorf("expected ~0.7 success rate, got %f", fitness.SuccessRate)
	}
}

func TestRecommendUCB1(t *testing.T) {
	eng, _ := setupEngine(t)
	ctx := context.Background()

	// Species A: 80% success, many runs
	_ = eng.RegisterSpecies(ctx, Species{
		ID: "a", Name: "a", Agent: "claude", Model: "sonnet-4", Tier: "fast",
	})
	for i := 0; i < 40; i++ {
		_ = eng.RecordOutcome(ctx, "a", true, 1.0, 30.0, 5000)
	}
	for i := 0; i < 10; i++ {
		_ = eng.RecordOutcome(ctx, "a", false, -1.0, 60.0, 8000)
	}

	// Species B: 100% success but only 2 runs
	_ = eng.RegisterSpecies(ctx, Species{
		ID: "b", Name: "b", Agent: "codex", Model: "gpt-4.1", Tier: "fast",
	})
	for i := 0; i < 2; i++ {
		_ = eng.RecordOutcome(ctx, "b", true, 1.0, 20.0, 3000)
	}

	// Species C: never tested
	_ = eng.RegisterSpecies(ctx, Species{
		ID: "c", Name: "c", Agent: "gemini", Model: "pro", Tier: "fast",
	})

	// With UCB1, untested species C should be recommended (infinite exploration bonus)
	rec, err := eng.Recommend(ctx, "fast")
	if err != nil {
		t.Fatal(err)
	}
	if rec.SpeciesID != "c" {
		t.Errorf("expected untested species 'c' to be recommended, got %q", rec.SpeciesID)
	}

	// Give C a terrible result
	_ = eng.RecordOutcome(ctx, "c", false, -1.0, 120.0, 10000)

	// Now B should win (100% success + exploration bonus from few runs)
	rec, err = eng.Recommend(ctx, "fast")
	if err != nil {
		t.Fatal(err)
	}
	if rec.SpeciesID != "b" {
		t.Errorf("expected 'b' (100%% success, few runs) to be recommended, got %q (score: %f)", rec.SpeciesID, rec.Fitness.UCBScore)
	}
}

func TestRecommendFiltersByTier(t *testing.T) {
	eng, _ := setupEngine(t)
	ctx := context.Background()

	_ = eng.RegisterSpecies(ctx, Species{
		ID: "fast-one", Name: "fast", Agent: "claude", Model: "haiku", Tier: "fast",
	})
	_ = eng.RegisterSpecies(ctx, Species{
		ID: "premium-one", Name: "premium", Agent: "claude", Model: "opus", Tier: "premium",
	})

	rec, err := eng.Recommend(ctx, "premium")
	if err != nil {
		t.Fatal(err)
	}
	if rec.SpeciesID != "premium-one" {
		t.Errorf("expected premium species, got %q", rec.SpeciesID)
	}
}

func TestToolPatterns(t *testing.T) {
	eng, store := setupEngine(t)
	ctx := context.Background()

	_ = eng.RegisterSpecies(ctx, Species{
		ID: "test", Name: "test", Agent: "claude", Model: "sonnet", Tier: "fast",
	})

	// Record the same tool sequence multiple times with good reward
	seq := []string{"read_file", "edit_file", "exec"}
	for i := 0; i < 5; i++ {
		_ = eng.RecordToolSequence(ctx, "test", seq, 0.9)
	}

	// Record a different sequence with bad reward
	badSeq := []string{"exec", "exec", "exec", "exec"}
	for i := 0; i < 3; i++ {
		_ = eng.RecordToolSequence(ctx, "test", badSeq, -0.5)
	}

	patterns, err := store.GetTopToolPatterns(ctx, "test", 5)
	if err != nil {
		t.Fatal(err)
	}

	if len(patterns) == 0 {
		t.Fatal("expected at least one pattern")
	}

	// Best pattern should be the successful one
	if patterns[0].AvgReward < 0 {
		t.Errorf("top pattern should have positive reward, got %f", patterns[0].AvgReward)
	}
}

func TestMutate(t *testing.T) {
	eng, store := setupEngine(t)
	ctx := context.Background()

	_ = eng.RegisterSpecies(ctx, Species{
		ID:          "parent",
		Name:        "parent",
		Agent:       "claude",
		Model:       "sonnet-4",
		Tier:        "fast",
		PromptStyle: "minimal",
		ToolSet:     []string{"read_file", "write_file", "exec"},
	})

	child, err := eng.Mutate(ctx, "parent", map[string]string{
		"model":       "opus-4",
		"tier":        "premium",
		"prompt_style": "chain-of-thought",
		"add_tool":    "web_search",
	})
	if err != nil {
		t.Fatal(err)
	}

	if child.Model != "opus-4" {
		t.Errorf("expected model opus-4, got %s", child.Model)
	}
	if child.Tier != "premium" {
		t.Errorf("expected tier premium, got %s", child.Tier)
	}
	if child.ParentID != "parent" {
		t.Errorf("expected parent_id 'parent', got %s", child.ParentID)
	}
	if len(child.ToolSet) != 4 {
		t.Errorf("expected 4 tools, got %d", len(child.ToolSet))
	}

	// Verify it's persisted
	fetched, err := store.GetSpecies(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.ParentID != "parent" {
		t.Errorf("persisted parent_id should be 'parent', got %s", fetched.ParentID)
	}
}

func TestLeaderboard(t *testing.T) {
	eng, _ := setupEngine(t)
	ctx := context.Background()

	_ = eng.RegisterSpecies(ctx, Species{ID: "s1", Name: "s1", Agent: "a", Model: "m1", Tier: "fast"})
	_ = eng.RegisterSpecies(ctx, Species{ID: "s2", Name: "s2", Agent: "b", Model: "m2", Tier: "fast"})
	_ = eng.RegisterSpecies(ctx, Species{ID: "s3", Name: "s3", Agent: "c", Model: "m3", Tier: "fast"})

	for i := 0; i < 20; i++ {
		_ = eng.RecordOutcome(ctx, "s1", true, 0.9, 25.0, 4000)
	}
	for i := 0; i < 10; i++ {
		_ = eng.RecordOutcome(ctx, "s2", true, 0.5, 40.0, 6000)
	}
	for i := 0; i < 5; i++ {
		_ = eng.RecordOutcome(ctx, "s2", false, -0.3, 50.0, 7000)
	}
	// s3: no runs at all

	board, err := eng.Leaderboard(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(board) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(board))
	}

	// s3 should be first (untested = infinite UCB)
	if board[0].SpeciesID != "s3" {
		t.Errorf("expected untested s3 first, got %s", board[0].SpeciesID)
	}

	formatted := FormatLeaderboard(board)
	if formatted == "" {
		t.Error("leaderboard formatting returned empty")
	}
}

func TestNoSpeciesError(t *testing.T) {
	eng, _ := setupEngine(t)
	ctx := context.Background()

	_, err := eng.Recommend(ctx, "")
	if err == nil {
		t.Error("expected error when no species registered")
	}
}

func TestFormatToolPattern(t *testing.T) {
	result := FormatToolPattern([]string{"read_file", "edit_file", "exec"})
	expected := "read_file → edit_file → exec"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

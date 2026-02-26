package admit

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// mockParser returns pre-built ParsedFile data.
type mockParser struct {
	files []*astpkg.ParsedFile
}

func (m *mockParser) ParseDir(_ context.Context, _ string) ([]*astpkg.ParsedFile, error) {
	return m.files, nil
}

func setupTestDAG(t *testing.T) *dag.DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	d := dag.NewDAG(db)
	if err := d.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return d
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestRunGate_PromotesValidTask(t *testing.T) {
	d := setupTestDAG(t)
	ctx := context.Background()

	_, err := d.CreateTask(ctx, dag.Task{
		ID:              "t-1",
		Title:           "Add caching",
		Description:     "Add mtime-based caching to the Parser.ParseFile method to avoid reparsing.",
		Acceptance:      "ParseFile returns cached result when mtime unchanged.",
		EstimateMinutes: 15,
		Type:            "task",
		Status:          "open",
		Project:         "proj",
	})
	if err != nil {
		t.Fatal(err)
	}

	parser := &mockParser{files: testFiles()}
	result, err := RunGate(ctx, d, parser, "proj", "/tmp", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if result.Promoted != 1 {
		t.Errorf("Promoted = %d, want 1", result.Promoted)
	}

	task, _ := d.GetTask(ctx, "t-1")
	if task.Status != "ready" {
		t.Errorf("task status = %s, want ready", task.Status)
	}

	targets, _ := d.GetTaskTargets(ctx, "t-1")
	if len(targets) == 0 {
		t.Error("expected targets to be stored")
	}
}

func TestRunGate_RejectsInvalidTask(t *testing.T) {
	d := setupTestDAG(t)
	ctx := context.Background()

	// Missing acceptance criteria
	_, _ = d.CreateTask(ctx, dag.Task{
		ID:              "t-bad",
		Title:           "Fix bug",
		Description:     "This task has enough description text to pass the length check easily.",
		Acceptance:      "",
		EstimateMinutes: 10,
		Type:            "task",
		Status:          "open",
		Project:         "proj",
	})

	parser := &mockParser{files: testFiles()}
	result, err := RunGate(ctx, d, parser, "proj", "/tmp", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsRefinement != 1 {
		t.Errorf("NeedsRefinement = %d, want 1", result.NeedsRefinement)
	}

	task, _ := d.GetTask(ctx, "t-bad")
	if task.Status != "needs_refinement" {
		t.Errorf("task status = %s, want needs_refinement", task.Status)
	}
}

func TestRunGate_DetectsStaleness(t *testing.T) {
	d := setupTestDAG(t)
	ctx := context.Background()

	// Create a task that's already ready with stored targets
	_, _ = d.CreateTask(ctx, dag.Task{
		ID:              "t-stale",
		Title:           "Modify OldFunction",
		Description:     "Modify OldFunction in old_code.go to handle edge cases properly.",
		Acceptance:      "OldFunction handles nil input.",
		EstimateMinutes: 10,
		Type:            "task",
		Status:          "ready",
		Project:         "proj",
	})
	// Store old targets pointing at a symbol that no longer exists
	_ = d.SetTaskTargets(ctx, "t-stale", []dag.TaskTarget{
		{TaskID: "t-stale", FilePath: "old_code.go", SymbolName: "OldFunction", SymbolKind: "func"},
	})

	// Current codebase doesn't have OldFunction
	parser := &mockParser{files: testFiles()}
	result, err := RunGate(ctx, d, parser, "proj", "/tmp", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if result.MarkedStale != 1 {
		t.Errorf("MarkedStale = %d, want 1", result.MarkedStale)
	}

	task, _ := d.GetTask(ctx, "t-stale")
	if task.Status != "stale" {
		t.Errorf("task status = %s, want stale", task.Status)
	}
}

func TestRunGate_AddsFences(t *testing.T) {
	d := setupTestDAG(t)
	ctx := context.Background()

	// Two ready tasks touching the same symbol
	for _, id := range []string{"t-1", "t-2"} {
		_, _ = d.CreateTask(ctx, dag.Task{
			ID:              id,
			Title:           "Work on Parser",
			Description:     "Modify the Parser struct in parser.go to add new functionality for caching.",
			Acceptance:      "Parser has new field.",
			EstimateMinutes: 15,
			Type:            "task",
			Status:          "ready",
			Project:         "proj",
			Priority:        0,
		})
	}
	// Give t-2 lower priority
	_ = d.UpdateTask(ctx, "t-2", map[string]any{"priority": 1})

	// Store targets for both
	_ = d.SetTaskTargets(ctx, "t-1", []dag.TaskTarget{
		{TaskID: "t-1", FilePath: "internal/ast/parser.go", SymbolName: "Parser", SymbolKind: "type"},
	})
	_ = d.SetTaskTargets(ctx, "t-2", []dag.TaskTarget{
		{TaskID: "t-2", FilePath: "internal/ast/parser.go", SymbolName: "Parser", SymbolKind: "type"},
	})

	parser := &mockParser{files: testFiles()}
	result, err := RunGate(ctx, d, parser, "proj", "/tmp", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if result.FencesAdded < 1 {
		t.Errorf("FencesAdded = %d, want >= 1", result.FencesAdded)
	}
}

func TestRunGate_CleansOldFences(t *testing.T) {
	d := setupTestDAG(t)
	ctx := context.Background()

	// Create two tasks with an old AST fence
	for _, id := range []string{"t-a", "t-b"} {
		_, _ = d.CreateTask(ctx, dag.Task{
			ID:              id,
			Title:           "Some task",
			Description:     "A task with enough description to pass validation checks easily.",
			Acceptance:      "Done.",
			EstimateMinutes: 10,
			Type:            "task",
			Status:          "ready",
			Project:         "proj",
		})
	}
	_ = d.AddEdgeWithSource(ctx, "t-b", "t-a", "ast")

	// No overlapping targets in this run — fence should be cleaned
	_ = d.SetTaskTargets(ctx, "t-a", []dag.TaskTarget{
		{TaskID: "t-a", FilePath: "internal/ast/parser.go", SymbolName: "Parser"},
	})
	_ = d.SetTaskTargets(ctx, "t-b", []dag.TaskTarget{
		{TaskID: "t-b", FilePath: "internal/dag/dag.go", SymbolName: "DAG"},
	})

	parser := &mockParser{files: testFiles()}
	result, err := RunGate(ctx, d, parser, "proj", "/tmp", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	// Old AST fence was cleaned, no new fences needed (different targets)
	if result.FencesAdded != 0 {
		t.Errorf("FencesAdded = %d, want 0 (old fence cleaned, no new overlap)", result.FencesAdded)
	}
}

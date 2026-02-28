package beadsync

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"

	_ "modernc.org/sqlite"
)

// stubLister implements IssueLister for testing.
type stubLister struct {
	issues []beads.Issue
	err    error
}

func (s *stubLister) List(_ context.Context, _ int) ([]beads.Issue, error) {
	return s.issues, s.err
}

func newTestDAG(t *testing.T) *dag.DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	d := dag.NewDAG(db)
	if err := d.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

func TestSyncToDAG_CreatesNewTasks(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	client := &stubLister{issues: []beads.Issue{
		{ID: "test-1", Title: "Task One", Description: "Desc one", Status: "open"},
		{ID: "test-2", Title: "Task Two", Description: "Desc two", Status: "ready"},
	}}

	result, err := SyncToDAG(context.Background(), client, d, "proj", testLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Created != 2 {
		t.Errorf("Created = %d, want 2", result.Created)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", result.Skipped)
	}

	// Verify tasks exist in DAG
	task, err := d.GetTask(context.Background(), "test-1")
	if err != nil {
		t.Fatalf("GetTask test-1: %v", err)
	}
	if task.Title != "Task One" {
		t.Errorf("Title = %q, want %q", task.Title, "Task One")
	}
	if task.Project != "proj" {
		t.Errorf("Project = %q, want %q", task.Project, "proj")
	}
}

func TestSyncToDAG_SkipsClosedAndCompleted(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	client := &stubLister{issues: []beads.Issue{
		{ID: "open-1", Title: "Open", Status: "open"},
		{ID: "closed-1", Title: "Closed", Status: "closed"},
		{ID: "done-1", Title: "Done", Status: "done"},
		{ID: "completed-1", Title: "Completed", Status: "completed"},
	}}

	result, err := SyncToDAG(context.Background(), client, d, "proj", testLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want 1", result.Created)
	}
	if result.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3", result.Skipped)
	}
}

func TestSyncToDAG_UpdatesChangedTask(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// Pre-create a task in the DAG
	_, err := d.CreateTask(ctx, dag.Task{
		ID: "test-1", Title: "Old Title", Description: "Old desc",
		Status: "open", Project: "proj",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	client := &stubLister{issues: []beads.Issue{
		{ID: "test-1", Title: "New Title", Description: "New desc", Status: "open"},
	}}

	result, err := SyncToDAG(ctx, client, d, "proj", testLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}

	// Verify update applied
	task, err := d.GetTask(ctx, "test-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Title != "New Title" {
		t.Errorf("Title = %q, want %q", task.Title, "New Title")
	}
}

func TestSyncToDAG_SkipsUnchangedTask(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, err := d.CreateTask(ctx, dag.Task{
		ID: "test-1", Title: "Same Title", Description: "Same desc",
		Acceptance: "criteria", Status: "open", Project: "proj",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	client := &stubLister{issues: []beads.Issue{
		{ID: "test-1", Title: "Same Title", Description: "Same desc",
			AcceptanceCriteria: "criteria", Status: "open"},
	}}

	result, err := SyncToDAG(ctx, client, d, "proj", testLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0", result.Updated)
	}
}

func TestSyncToDAG_ImportsDependencyEdges(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	client := &stubLister{issues: []beads.Issue{
		{ID: "parent-1", Title: "Parent", Status: "open"},
		{ID: "child-1", Title: "Child", Status: "open", Dependencies: []beads.Dependency{
			{IssueID: "child-1", DependsOnID: "parent-1"},
		}},
	}}

	result, err := SyncToDAG(ctx, client, d, "proj", testLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Created != 2 {
		t.Errorf("Created = %d, want 2", result.Created)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want none", result.Errors)
	}
}

func TestSyncToDAG_SkipsEdgeToClosedDependency(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// An open issue depends on a closed issue — the closed issue won't be
	// in the DAG, so the edge should be silently skipped (no FK violation).
	client := &stubLister{issues: []beads.Issue{
		{ID: "closed-1", Title: "Done Task", Status: "closed"},
		{ID: "open-1", Title: "Open Task", Status: "open", Dependencies: []beads.Dependency{
			{IssueID: "open-1", DependsOnID: "closed-1"},
		}},
	}}

	result, err := SyncToDAG(ctx, client, d, "proj", testLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want 1", result.Created)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want none", result.Errors)
	}
}

func TestSyncToDAG_ListError(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	client := &stubLister{err: context.DeadlineExceeded}

	_, err := SyncToDAG(context.Background(), client, d, "proj", testLogger)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSyncToDAG_BuildDescriptionWithDesign(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	client := &stubLister{issues: []beads.Issue{
		{ID: "test-1", Title: "With Design", Description: "Main desc",
			Design: "Some design notes", Status: "open"},
	}}

	_, err := SyncToDAG(context.Background(), client, d, "proj", testLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task, err := d.GetTask(context.Background(), "test-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	want := "Main desc\n\nDesign:\nSome design notes"
	if task.Description != want {
		t.Errorf("Description = %q, want %q", task.Description, want)
	}
}

func TestSyncResult_String(t *testing.T) {
	t.Parallel()
	r := SyncResult{Created: 3, Updated: 1, Skipped: 2, Errors: []string{"e1"}}
	got := r.String()
	want := "created=3 updated=1 skipped=2 errors=1"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

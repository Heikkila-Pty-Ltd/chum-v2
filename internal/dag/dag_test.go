package dag

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDAG returns a DAG backed by an in-memory SQLite database.
func newTestDAG(t *testing.T) *DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	d := NewDAG(db)
	if err := d.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// --- TaskStore interface ---

func TestDAG_SatisfiesTaskStore(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	// Compile-time check is in store.go (var _ TaskStore = (*DAG)(nil)),
	// but this runtime test verifies the concrete value works.
	var _ TaskStore = d
}

// --- EnsureSchema ---

func TestEnsureSchema_Idempotent(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// Calling EnsureSchema a second time should be a no-op.
	if err := d.EnsureSchema(ctx); err != nil {
		t.Fatalf("second EnsureSchema call failed: %v", err)
	}
}

// --- CreateTask / GetTask ---

func TestCreateTask_GeneratesID(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	id, err := d.CreateTask(ctx, Task{
		Title:   "test task",
		Project: "myproject",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if id == "" {
		t.Fatal("expected generated ID, got empty")
	}
}

func TestCreateTask_UsesExplicitID(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	id, err := d.CreateTask(ctx, Task{
		ID:      "explicit-1",
		Title:   "explicit",
		Project: "p",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if id != "explicit-1" {
		t.Fatalf("ID = %q, want explicit-1", id)
	}
}

func TestCreateTask_DefaultsStatusAndType(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, Task{ID: "def-1", Project: "p"})
	got, err := d.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != "open" {
		t.Fatalf("Status = %q, want open", got.Status)
	}
	if got.Type != "task" {
		t.Fatalf("Type = %q, want task", got.Type)
	}
}

func TestGetTask_RoundTrips(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, Task{
		ID:              "rt-1",
		Title:           "roundtrip",
		Description:     "desc",
		Status:          "ready",
		Priority:        5,
		Type:            "bug",
		Assignee:        "bot",
		Labels:          []string{"critical", "backend"},
		EstimateMinutes: 30,
		ParentID:        "parent-0",
		Acceptance:      "tests pass",
		Project:         "proj",
		ErrorLog:        "none",
	})

	got, err := d.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "roundtrip" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Description != "desc" {
		t.Fatalf("Description = %q", got.Description)
	}
	if got.Status != "ready" {
		t.Fatalf("Status = %q", got.Status)
	}
	if got.Priority != 5 {
		t.Fatalf("Priority = %d", got.Priority)
	}
	if got.Type != "bug" {
		t.Fatalf("Type = %q", got.Type)
	}
	if got.Assignee != "bot" {
		t.Fatalf("Assignee = %q", got.Assignee)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "critical" || got.Labels[1] != "backend" {
		t.Fatalf("Labels = %v", got.Labels)
	}
	if got.EstimateMinutes != 30 {
		t.Fatalf("EstimateMinutes = %d", got.EstimateMinutes)
	}
	if got.ParentID != "parent-0" {
		t.Fatalf("ParentID = %q", got.ParentID)
	}
	if got.Acceptance != "tests pass" {
		t.Fatalf("Acceptance = %q", got.Acceptance)
	}
	if got.Project != "proj" {
		t.Fatalf("Project = %q", got.Project)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, err := d.GetTask(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

// --- ListTasks ---

func TestListTasks_FiltersByProject(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "a-1", Project: "alpha", Title: "a1"})
	_, _ = d.CreateTask(ctx, Task{ID: "b-1", Project: "beta", Title: "b1"})
	_, _ = d.CreateTask(ctx, Task{ID: "a-2", Project: "alpha", Title: "a2"})

	tasks, err := d.ListTasks(ctx, "alpha")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len = %d, want 2", len(tasks))
	}
}

func TestListTasks_FiltersByStatus(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "s-1", Project: "p", Status: "ready"})
	_, _ = d.CreateTask(ctx, Task{ID: "s-2", Project: "p", Status: "running"})
	_, _ = d.CreateTask(ctx, Task{ID: "s-3", Project: "p", Status: "ready"})
	_, _ = d.CreateTask(ctx, Task{ID: "s-4", Project: "p", Status: "completed"})

	tasks, err := d.ListTasks(ctx, "p", "ready", "completed")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("len = %d, want 3", len(tasks))
	}
}

func TestListTasks_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	tasks, err := d.ListTasks(ctx, "empty-project")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("len = %d, want 0", len(tasks))
	}
}

// --- UpdateTask ---

func TestUpdateTask_UpdatesFields(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "u-1", Project: "p", Title: "old"})

	err := d.UpdateTask(ctx, "u-1", map[string]any{
		"title":       "new",
		"description": "updated desc",
		"priority":    3,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	got, _ := d.GetTask(ctx, "u-1")
	if got.Title != "new" {
		t.Fatalf("Title = %q, want new", got.Title)
	}
	if got.Description != "updated desc" {
		t.Fatalf("Description = %q", got.Description)
	}
	if got.Priority != 3 {
		t.Fatalf("Priority = %d, want 3", got.Priority)
	}
}

func TestUpdateTask_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "u-2", Project: "p"})
	err := d.UpdateTask(ctx, "u-2", map[string]any{"bogus": "value"})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestUpdateTask_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	err := d.UpdateTask(ctx, "missing", map[string]any{"title": "x"})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestUpdateTask_EmptyFieldsIsNoop(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	err := d.UpdateTask(ctx, "whatever", map[string]any{})
	if err != nil {
		t.Fatalf("empty fields should be no-op, got %v", err)
	}
}

func TestUpdateTask_LabelsMarshalled(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "lbl-1", Project: "p"})
	err := d.UpdateTask(ctx, "lbl-1", map[string]any{
		"labels": []string{"frontend", "urgent"},
	})
	if err != nil {
		t.Fatalf("UpdateTask labels: %v", err)
	}

	got, _ := d.GetTask(ctx, "lbl-1")
	if len(got.Labels) != 2 || got.Labels[0] != "frontend" || got.Labels[1] != "urgent" {
		t.Fatalf("Labels = %v, want [frontend urgent]", got.Labels)
	}
}

// --- UpdateTaskStatus / CloseTask ---

func TestUpdateTaskStatus(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "st-1", Project: "p", Status: "open"})
	if err := d.UpdateTaskStatus(ctx, "st-1", "running"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	got, _ := d.GetTask(ctx, "st-1")
	if got.Status != "running" {
		t.Fatalf("Status = %q, want running", got.Status)
	}
}

func TestCloseTask(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "cl-1", Project: "p", Status: "running"})
	if err := d.CloseTask(ctx, "cl-1", "completed"); err != nil {
		t.Fatalf("CloseTask: %v", err)
	}

	got, _ := d.GetTask(ctx, "cl-1")
	if got.Status != "completed" {
		t.Fatalf("Status = %q, want completed", got.Status)
	}
}

// --- Edges ---

func TestAddEdge_SelfEdgeRejected(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// Create the task first so FK constraints don't mask a broken self-edge guard.
	_, _ = d.CreateTask(ctx, Task{ID: "self-1", Project: "p"})

	err := d.AddEdge(ctx, "self-1", "self-1")
	if err == nil {
		t.Fatal("expected error for self-edge")
	}
	if !strings.Contains(err.Error(), "self-edge") {
		t.Fatalf("expected self-edge error, got: %v", err)
	}
}

func TestAddEdge_RemoveEdge(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "e-1", Project: "p"})
	_, _ = d.CreateTask(ctx, Task{ID: "e-2", Project: "p"})

	if err := d.AddEdge(ctx, "e-1", "e-2"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	// Idempotent — second insert should not error (INSERT OR IGNORE).
	if err := d.AddEdge(ctx, "e-1", "e-2"); err != nil {
		t.Fatalf("AddEdge idempotent: %v", err)
	}

	if err := d.RemoveEdge(ctx, "e-1", "e-2"); err != nil {
		t.Fatalf("RemoveEdge: %v", err)
	}
}

func TestGetDependents(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "gd-parent", Project: "p"})
	_, _ = d.CreateTask(ctx, Task{ID: "gd-child1", Project: "p"})
	_, _ = d.CreateTask(ctx, Task{ID: "gd-child2", Project: "p"})

	_ = d.AddEdge(ctx, "gd-child1", "gd-parent") // child1 depends on parent
	_ = d.AddEdge(ctx, "gd-child2", "gd-parent") // child2 depends on parent

	deps, err := d.GetDependents(ctx, "gd-parent")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependents, got %v", deps)
	}

	// No dependents case
	deps, err = d.GetDependents(ctx, "gd-child1")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependents, got %v", deps)
	}
}

func TestCreateSubtasksAtomic_RewiresEdges(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// Setup: prereq → parent → downstream
	_, _ = d.CreateTask(ctx, Task{ID: "prereq", Project: "p", Status: "completed"})
	_, _ = d.CreateTask(ctx, Task{ID: "parent", Project: "p", Status: "running"})
	_, _ = d.CreateTask(ctx, Task{ID: "downstream", Project: "p", Status: "ready"})
	_ = d.AddEdge(ctx, "parent", "prereq")     // parent depends on prereq
	_ = d.AddEdge(ctx, "downstream", "parent") // downstream depends on parent

	// Decompose parent into 2 subtasks
	ids, err := d.CreateSubtasksAtomic(ctx, "parent", []Task{
		{Title: "S1", Description: "step 1", Project: "p"},
		{Title: "S2", Description: "step 2", Project: "p"},
	})
	if err != nil {
		t.Fatalf("CreateSubtasksAtomic: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 subtask IDs, got %d", len(ids))
	}

	// Parent should be "decomposed"
	parent, _ := d.GetTask(ctx, "parent")
	if parent.Status != "decomposed" {
		t.Fatalf("parent status = %q, want decomposed", parent.Status)
	}

	// S1 should inherit prereq dependency
	s1Deps, _ := d.GetDependents(ctx, "prereq")
	foundS1 := false
	for _, dep := range s1Deps {
		if dep == ids[0] {
			foundS1 = true
		}
	}
	if !foundS1 {
		t.Fatalf("S1 (%s) should depend on prereq, dependents of prereq = %v", ids[0], s1Deps)
	}

	// Downstream should now depend on S2 (last subtask), not parent
	s2Deps, _ := d.GetDependents(ctx, ids[1])
	foundDownstream := false
	for _, dep := range s2Deps {
		if dep == "downstream" {
			foundDownstream = true
		}
	}
	if !foundDownstream {
		t.Fatalf("downstream should depend on S2 (%s), dependents of S2 = %v", ids[1], s2Deps)
	}

	// Downstream should NOT depend on parent anymore
	parentDeps, _ := d.GetDependents(ctx, "parent")
	for _, dep := range parentDeps {
		if dep == "downstream" {
			t.Fatal("downstream should no longer depend on parent")
		}
	}

	// S2 should depend on S1 (sequential wiring)
	s1AsDep, _ := d.GetDependents(ctx, ids[0])
	foundS2 := false
	for _, dep := range s1AsDep {
		if dep == ids[1] {
			foundS2 = true
		}
	}
	if !foundS2 {
		t.Fatalf("S2 should depend on S1, dependents of S1 = %v", s1AsDep)
	}

	// Subtasks should be created as "open" (admission gate promotes them)
	s1, _ := d.GetTask(ctx, ids[0])
	if s1.Status != "open" {
		t.Fatalf("S1 status = %q, want open", s1.Status)
	}

	// Parent's upstream edges should be cleaned up (no dangling cruft)
	parentPrereqDeps, _ := d.GetDependents(ctx, "prereq")
	for _, dep := range parentPrereqDeps {
		if dep == "parent" {
			t.Fatal("parent→prereq edge should be removed after decomposition")
		}
	}

	// Parent's downstream edges should also be cleaned up
	parentDownDeps, _ := d.GetDependents(ctx, "parent")
	if len(parentDownDeps) != 0 {
		t.Fatalf("parent should have no dependents after decomposition, got %v", parentDownDeps)
	}

	// Edge source should be preserved (original edges were 'beads')
	source, err := d.GetEdgeSource(ctx, ids[0], "prereq")
	if err != nil {
		t.Fatalf("get inherited edge source: %v", err)
	}
	if source != "beads" {
		t.Fatalf("inherited edge source = %q, want beads", source)
	}
}

func TestAddEdgeWithSource(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "es-1", Project: "p"})
	_, _ = d.CreateTask(ctx, Task{ID: "es-2", Project: "p"})

	if err := d.AddEdgeWithSource(ctx, "es-1", "es-2", "ast"); err != nil {
		t.Fatalf("AddEdgeWithSource: %v", err)
	}
}

func TestDeleteEdgesBySource(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "ds-1", Project: "p"})
	_, _ = d.CreateTask(ctx, Task{ID: "ds-2", Project: "p"})
	_, _ = d.CreateTask(ctx, Task{ID: "ds-3", Project: "p"})

	_ = d.AddEdgeWithSource(ctx, "ds-1", "ds-2", "ast")
	_ = d.AddEdgeWithSource(ctx, "ds-1", "ds-3", "beads")

	if err := d.DeleteEdgesBySource(ctx, "p", "ast"); err != nil {
		t.Fatalf("DeleteEdgesBySource: %v", err)
	}

	// Phase 1: ds-1 should still be blocked by ds-3 (beads edge remains).
	_ = d.UpdateTaskStatus(ctx, "ds-1", "ready")
	_ = d.UpdateTaskStatus(ctx, "ds-3", "open") // dep not completed
	ready, _ := d.GetReadyNodes(ctx, "p")
	for _, r := range ready {
		if r.ID == "ds-1" {
			t.Fatal("ds-1 should still be blocked by ds-3 (beads edge)")
		}
	}

	// Phase 2: Complete ds-3 → ds-1 should become ready, proving the
	// ast edge (ds-1 → ds-2) was actually removed. If it weren't, ds-1
	// would still be blocked by the uncompleted ds-2.
	_ = d.UpdateTaskStatus(ctx, "ds-3", "completed")
	ready, _ = d.GetReadyNodes(ctx, "p")
	found := false
	for _, r := range ready {
		if r.ID == "ds-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("ds-1 should be ready after ds-3 completed (ast edge to ds-2 should be gone)")
	}
}

// --- GetReadyNodes ---

func TestGetReadyNodes_NoDeps(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "r-1", Project: "p", Status: "ready"})
	_, _ = d.CreateTask(ctx, Task{ID: "r-2", Project: "p", Status: "open"})

	ready, err := d.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "r-1" {
		t.Fatalf("ready = %v, want [r-1]", taskIDs(ready))
	}
}

func TestGetReadyNodes_BlockedByDep(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "dep-a", Project: "p", Status: "open"})
	_, _ = d.CreateTask(ctx, Task{ID: "dep-b", Project: "p", Status: "ready"})
	_ = d.AddEdge(ctx, "dep-b", "dep-a") // b depends on a

	ready, err := d.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	// dep-b is "ready" but blocked by dep-a (not completed).
	if len(ready) != 0 {
		t.Fatalf("expected no ready nodes, got %v", taskIDs(ready))
	}
}

func TestGetReadyNodes_UnblockedWhenDepCompleted(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "uc-a", Project: "p", Status: "completed"})
	_, _ = d.CreateTask(ctx, Task{ID: "uc-b", Project: "p", Status: "ready"})
	_ = d.AddEdge(ctx, "uc-b", "uc-a") // b depends on a

	ready, err := d.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "uc-b" {
		t.Fatalf("ready = %v, want [uc-b]", taskIDs(ready))
	}
}

func TestGetReadyNodes_PriorityOrdering(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "pr-low", Project: "p", Status: "ready", Priority: 10})
	_, _ = d.CreateTask(ctx, Task{ID: "pr-high", Project: "p", Status: "ready", Priority: 1})

	ready, _ := d.GetReadyNodes(ctx, "p")
	if len(ready) != 2 {
		t.Fatalf("len = %d, want 2", len(ready))
	}
	if ready[0].ID != "pr-high" {
		t.Fatalf("expected pr-high first (priority 1), got %q", ready[0].ID)
	}
}

func TestGetReadyNodes_MultipleDeps_AllMustComplete(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "md-a", Project: "p", Status: "completed"})
	_, _ = d.CreateTask(ctx, Task{ID: "md-b", Project: "p", Status: "open"})
	_, _ = d.CreateTask(ctx, Task{ID: "md-c", Project: "p", Status: "ready"})
	_ = d.AddEdge(ctx, "md-c", "md-a")
	_ = d.AddEdge(ctx, "md-c", "md-b")

	ready, _ := d.GetReadyNodes(ctx, "p")
	if len(ready) != 0 {
		t.Fatalf("expected 0 ready (md-b still open), got %v", taskIDs(ready))
	}

	// Complete md-b → md-c should now be ready.
	_ = d.UpdateTaskStatus(ctx, "md-b", "completed")
	ready, _ = d.GetReadyNodes(ctx, "p")
	if len(ready) != 1 || ready[0].ID != "md-c" {
		t.Fatalf("ready = %v, want [md-c]", taskIDs(ready))
	}
}

func TestGetReadyNodes_FiltersByProject(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "fp-1", Project: "alpha", Status: "ready"})
	_, _ = d.CreateTask(ctx, Task{ID: "fp-2", Project: "beta", Status: "ready"})

	ready, _ := d.GetReadyNodes(ctx, "alpha")
	if len(ready) != 1 || ready[0].ID != "fp-1" {
		t.Fatalf("ready = %v, want [fp-1]", taskIDs(ready))
	}
}

// --- TaskTargets ---

func TestSetAndGetTaskTargets(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "tgt-1", Project: "p"})

	targets := []TaskTarget{
		{TaskID: "tgt-1", FilePath: "main.go", SymbolName: "main", SymbolKind: "function"},
		{TaskID: "tgt-1", FilePath: "lib.go", SymbolName: "Foo", SymbolKind: "struct"},
	}
	if err := d.SetTaskTargets(ctx, "tgt-1", targets); err != nil {
		t.Fatalf("SetTaskTargets: %v", err)
	}

	got, err := d.GetTaskTargets(ctx, "tgt-1")
	if err != nil {
		t.Fatalf("GetTaskTargets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestSetTaskTargets_Replaces(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "rep-1", Project: "p"})

	_ = d.SetTaskTargets(ctx, "rep-1", []TaskTarget{
		{TaskID: "rep-1", FilePath: "old.go"},
	})
	_ = d.SetTaskTargets(ctx, "rep-1", []TaskTarget{
		{TaskID: "rep-1", FilePath: "new.go"},
	})

	got, _ := d.GetTaskTargets(ctx, "rep-1")
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].FilePath != "new.go" {
		t.Fatalf("FilePath = %q, want new.go", got[0].FilePath)
	}
}

func TestGetAllTargetsForStatuses(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "at-1", Project: "p", Status: "ready"})
	_, _ = d.CreateTask(ctx, Task{ID: "at-2", Project: "p", Status: "running"})
	_, _ = d.CreateTask(ctx, Task{ID: "at-3", Project: "p", Status: "completed"})

	_ = d.SetTaskTargets(ctx, "at-1", []TaskTarget{
		{TaskID: "at-1", FilePath: "a.go"},
	})
	_ = d.SetTaskTargets(ctx, "at-2", []TaskTarget{
		{TaskID: "at-2", FilePath: "b.go"},
	})
	_ = d.SetTaskTargets(ctx, "at-3", []TaskTarget{
		{TaskID: "at-3", FilePath: "c.go"},
	})

	got, err := d.GetAllTargetsForStatuses(ctx, "p", "ready", "running")
	if err != nil {
		t.Fatalf("GetAllTargetsForStatuses: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 task IDs", len(got))
	}
	if _, ok := got["at-1"]; !ok {
		t.Fatal("missing at-1 targets")
	}
	if _, ok := got["at-2"]; !ok {
		t.Fatal("missing at-2 targets")
	}
	if _, ok := got["at-3"]; ok {
		t.Fatal("at-3 (completed) should not be included")
	}
}

func TestGetAllTargetsForStatuses_EmptyStatuses(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	got, err := d.GetAllTargetsForStatuses(ctx, "p")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCreateTask_WithMetadata(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	meta := map[string]string{"source": "jarvis", "priority_reason": "user_request"}
	_, err := d.CreateTask(ctx, Task{
		ID:       "meta-1",
		Title:    "task with metadata",
		Project:  "p",
		Metadata: meta,
	})
	if err != nil {
		t.Fatalf("CreateTask with metadata: %v", err)
	}

	got, err := d.GetTask(ctx, "meta-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}
	if got.Metadata["source"] != "jarvis" {
		t.Fatalf("Metadata[source] = %q, want jarvis", got.Metadata["source"])
	}
	if got.Metadata["priority_reason"] != "user_request" {
		t.Fatalf("Metadata[priority_reason] = %q", got.Metadata["priority_reason"])
	}
}

func TestCreateTask_NilMetadataRoundTrips(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, err := d.CreateTask(ctx, Task{ID: "meta-nil", Title: "no meta", Project: "p"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := d.GetTask(ctx, "meta-nil")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Metadata != nil {
		t.Fatalf("expected nil metadata, got %v", got.Metadata)
	}
}

func TestUpdateTask_Metadata(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "meta-upd", Title: "update meta", Project: "p"})

	err := d.UpdateTask(ctx, "meta-upd", map[string]any{
		"metadata": map[string]string{"updated": "true"},
	})
	if err != nil {
		t.Fatalf("UpdateTask metadata: %v", err)
	}

	got, err := d.GetTask(ctx, "meta-upd")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Metadata == nil || got.Metadata["updated"] != "true" {
		t.Fatalf("Metadata after update = %v", got.Metadata)
	}
}

func TestGlobalPauseState_DefaultFalse(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)

	paused, err := d.IsGlobalPaused(context.Background())
	if err != nil {
		t.Fatalf("IsGlobalPaused: %v", err)
	}
	if paused {
		t.Fatal("expected default global pause=false")
	}
}

func TestGlobalPauseState_SetAndRead(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	if err := d.SetGlobalPaused(ctx, true); err != nil {
		t.Fatalf("SetGlobalPaused(true): %v", err)
	}
	paused, err := d.IsGlobalPaused(ctx)
	if err != nil {
		t.Fatalf("IsGlobalPaused after set true: %v", err)
	}
	if !paused {
		t.Fatal("expected paused=true")
	}

	if err := d.SetGlobalPaused(ctx, false); err != nil {
		t.Fatalf("SetGlobalPaused(false): %v", err)
	}
	paused, err = d.IsGlobalPaused(ctx)
	if err != nil {
		t.Fatalf("IsGlobalPaused after set false: %v", err)
	}
	if paused {
		t.Fatal("expected paused=false")
	}
}

// --- helpers ---

func taskIDs(tasks []Task) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}

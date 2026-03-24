package engine

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

func TestFilterBeadsMappedReadyTasks_AutoBootstrapsPlannerTasks(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	d := dag.NewDAG(db)
	ctx := context.Background()
	if err := d.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// Create a ready task with NO beads mapping (simulates planner-created task)
	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "plan-subtask-1",
		Title:   "Planner subtask",
		Project: "proj",
		Status:  "ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	a := &DispatchActivities{
		DAG:    d,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: &config.Config{
			BeadsBridge: config.BeadsBridge{
				Enabled:       true,
				IngressPolicy: "beads_only",
			},
		},
		BeadsClients: nil, // no beads client — simulates planner-only creation
	}

	ready := []dag.Task{{
		ID:      "plan-subtask-1",
		Title:   "Planner subtask",
		Project: "proj",
		Status:  "ready",
	}}

	filtered := a.filterBeadsMappedReadyTasks(ctx, "proj", ready)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 task after auto-bootstrap, got %d", len(filtered))
	}

	// Verify the synthetic mapping was created
	mapping, err := d.GetBeadsMappingByTask(ctx, "proj", "plan-subtask-1")
	if err != nil {
		t.Fatalf("get mapping: %v", err)
	}
	if mapping.IssueID != "synthetic/plan-subtask-1" {
		t.Fatalf("expected synthetic issue ID, got %q", mapping.IssueID)
	}
}

func TestFilterBeadsMappedReadyTasks_ExistingMappingPasses(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	d := dag.NewDAG(db)
	ctx := context.Background()
	if err := d.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "beads-task-1",
		Title:   "Beads task",
		Project: "proj",
		Status:  "ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "proj", "bd-42", "beads-task-1", "fp"); err != nil {
		t.Fatalf("upsert mapping: %v", err)
	}

	a := &DispatchActivities{
		DAG: d,
		Config: &config.Config{
			BeadsBridge: config.BeadsBridge{
				Enabled:       true,
				IngressPolicy: "beads_only",
			},
		},
	}

	ready := []dag.Task{{
		ID:      "beads-task-1",
		Title:   "Beads task",
		Project: "proj",
		Status:  "ready",
	}}

	filtered := a.filterBeadsMappedReadyTasks(ctx, "proj", ready)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 task with existing mapping, got %d", len(filtered))
	}
}

func TestFilterBeadsMappedReadyTasks_LegacyPolicySkipsFilter(t *testing.T) {
	t.Parallel()
	a := &DispatchActivities{
		Config: &config.Config{
			BeadsBridge: config.BeadsBridge{
				Enabled:       true,
				IngressPolicy: "legacy",
			},
		},
	}

	ready := []dag.Task{{
		ID:      "any-task",
		Project: "proj",
		Status:  "ready",
	}}

	filtered := a.filterBeadsMappedReadyTasks(context.Background(), "proj", ready)
	if len(filtered) != 1 {
		t.Fatalf("legacy policy should skip filter, got %d", len(filtered))
	}
}

package beadsbridge

import (
	"context"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

type reconcileStore struct {
	issues []beads.Issue
}

func (s *reconcileStore) List(_ context.Context, _ int) ([]beads.Issue, error)  { return s.issues, nil }
func (s *reconcileStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) { return s.issues, nil }
func (s *reconcileStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	for _, is := range s.issues {
		if is.ID == issueID {
			return is, nil
		}
	}
	return beads.Issue{}, nil
}
func (s *reconcileStore) Close(_ context.Context, _, _ string) error { return nil }
func (s *reconcileStore) Create(_ context.Context, _ beads.CreateParams) (string, error) {
	return "", nil
}
func (s *reconcileStore) Update(_ context.Context, _ string, _ map[string]string) error { return nil }
func (s *reconcileStore) Children(_ context.Context, _ string) ([]beads.Issue, error) {
	return nil, nil
}
func (s *reconcileStore) AddDependency(_ context.Context, _, _ string) error { return nil }

func TestReconcileProject_DeterministicDriftReport(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	ctx := context.Background()

	// Mapped status mismatch.
	_, _ = d.CreateTask(ctx, dag.Task{
		ID:       "task-a",
		Project:  "proj",
		Title:    "A",
		Status:   "open",
		Metadata: map[string]string{"beads_bridge": "true"},
	})
	_ = d.UpsertBeadsMapping(ctx, "proj", "bd-a", "task-a", "fp-a")

	// Orphaned bridge task (no mapping).
	_, _ = d.CreateTask(ctx, dag.Task{
		ID:       "task-orphan",
		Project:  "proj",
		Title:    "Orphan",
		Status:   "open",
		Metadata: map[string]string{"beads_bridge": "true"},
	})

	store := &reconcileStore{
		issues: []beads.Issue{
			{ID: "bd-a", Title: "A", Status: "ready"},
			{ID: "bd-missing-map", Title: "M", Status: "ready"},
		},
	}

	r1, err := ReconcileProject(ctx, d, store, "proj", false, nil)
	if err != nil {
		t.Fatalf("reconcile first run: %v", err)
	}
	r2, err := ReconcileProject(ctx, d, store, "proj", false, nil)
	if err != nil {
		t.Fatalf("reconcile second run: %v", err)
	}
	if len(r1.Items) != len(r2.Items) {
		t.Fatalf("non-deterministic report length: %d vs %d", len(r1.Items), len(r2.Items))
	}
	for i := range r1.Items {
		if r1.Items[i] != r2.Items[i] {
			t.Fatalf("non-deterministic item[%d]: %+v vs %+v", i, r1.Items[i], r2.Items[i])
		}
	}
}

func TestReconcileProject_ApplyAllowlistedStatusFix(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, dag.Task{ID: "task-a", Project: "proj", Title: "A", Status: "open"})
	_ = d.UpsertBeadsMapping(ctx, "proj", "bd-a", "task-a", "fp-a")
	store := &reconcileStore{
		issues: []beads.Issue{
			{ID: "bd-a", Title: "A", Status: "ready"},
		},
	}
	_, err := ReconcileProject(ctx, d, store, "proj", true, map[DriftClass]bool{
		DriftStatusMismatch: true,
	})
	if err != nil {
		t.Fatalf("reconcile apply: %v", err)
	}
	task, err := d.GetTask(ctx, "task-a")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != "ready" {
		t.Fatalf("expected status reconcile apply to set ready, got %q", task.Status)
	}
}

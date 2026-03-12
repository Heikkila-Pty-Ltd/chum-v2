package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// closeTaskStore is a minimal mock for testing closeTask.
type closeTaskStore struct {
	dag.TaskStore
	task    dag.Task
	getErr  error
	updated map[string]any
	updErr  error
}

func (s *closeTaskStore) GetTask(_ context.Context, _ string) (dag.Task, error) {
	return s.task, s.getErr
}
func (s *closeTaskStore) UpdateTask(_ context.Context, _ string, fields map[string]any) error {
	s.updated = fields
	return s.updErr
}

func TestCloseTaskPreservesPRMetadata(t *testing.T) {
	prev := CloseDetail{
		Reason:    CloseFailed,
		PRNumber:  42,
		ReviewURL: "https://github.com/org/repo/pull/42",
	}
	prevRaw, _ := json.Marshal(prev)

	store := &closeTaskStore{
		task: dag.Task{ErrorLog: string(prevRaw)},
	}

	// New detail has no PR info — closeTask should preserve from previous.
	detail := CloseDetail{Reason: CloseFailed, SubReason: "exec_failed"}
	got, err := closeTask(context.Background(), store, "task-1", detail)
	if err != nil {
		t.Fatalf("closeTask error: %v", err)
	}
	if got.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", got.PRNumber)
	}
	if got.ReviewURL != "https://github.com/org/repo/pull/42" {
		t.Errorf("ReviewURL = %q, want preserved URL", got.ReviewURL)
	}
	// Verify DAG was updated with the correct status.
	if store.updated["status"] != string(CloseFailed) {
		t.Errorf("status = %v, want %v", store.updated["status"], CloseFailed)
	}
}

func TestCloseTaskDoesNotOverwriteExistingPR(t *testing.T) {
	store := &closeTaskStore{
		task: dag.Task{},
	}

	// Detail already has PR info — should keep it.
	detail := CloseDetail{
		Reason:    CloseCompleted,
		PRNumber:  99,
		ReviewURL: "https://github.com/org/repo/pull/99",
	}
	got, err := closeTask(context.Background(), store, "task-2", detail)
	if err != nil {
		t.Fatalf("closeTask error: %v", err)
	}
	if got.PRNumber != 99 {
		t.Errorf("PRNumber = %d, want 99", got.PRNumber)
	}
}

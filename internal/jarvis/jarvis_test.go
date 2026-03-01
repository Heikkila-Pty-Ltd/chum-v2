package jarvis

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

func testDAG(t *testing.T) *dag.DAG {
	t.Helper()
	d, err := dag.Open(":memory:")
	if err != nil {
		t.Fatalf("open dag: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSubmitCreatesTask(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	ctx := context.Background()
	id, err := e.Submit(ctx, WorkRequest{
		Title:       "Fix broken test",
		Description: "The TestFoo test is failing due to nil pointer",
		Project:     "chum",
		Source:      "jarvis-test",
		Labels:      []string{"bugfix"},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty task ID")
	}

	// Verify task in DAG.
	task, err := d.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Title != "Fix broken test" {
		t.Errorf("title = %q, want %q", task.Title, "Fix broken test")
	}
	if task.Status != "ready" {
		t.Errorf("status = %q, want %q", task.Status, "ready")
	}
	if task.Metadata["source"] != "jarvis-test" {
		t.Errorf("metadata[source] = %q, want %q", task.Metadata["source"], "jarvis-test")
	}

	// Should have jarvis-submitted label.
	hasLabel := false
	for _, l := range task.Labels {
		if l == "jarvis-submitted" {
			hasLabel = true
			break
		}
	}
	if !hasLabel {
		t.Error("missing jarvis-submitted label")
	}
}

func TestSubmitUnknownProject(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	_, err := e.Submit(context.Background(), WorkRequest{
		Title:   "Bad project",
		Project: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestSubmitDefaultSource(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	id, err := e.Submit(context.Background(), WorkRequest{
		Title:       "Test default source",
		Description: "desc",
		Project:     "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	task, err := d.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Metadata["source"] != "jarvis" {
		t.Errorf("default source = %q, want %q", task.Metadata["source"], "jarvis")
	}
}

func TestGetStatusReady(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	id, err := e.Submit(context.Background(), WorkRequest{
		Title:   "Status check",
		Project: "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	result, err := e.GetStatus(context.Background(), id)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if result.Status != "ready" {
		t.Errorf("status = %q, want %q", result.Status, "ready")
	}
	if result.TaskID != id {
		t.Errorf("task_id = %q, want %q", result.TaskID, id)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	_, err := e.GetStatus(context.Background(), "nonexistent-12345")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestListPendingFiltersJarvisTasks(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	ctx := context.Background()

	// Create a Jarvis task.
	_, err := e.Submit(ctx, WorkRequest{
		Title:   "Jarvis task",
		Project: "chum",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Create a non-Jarvis task directly.
	_, err = d.CreateTask(ctx, dag.Task{
		Title:   "Manual task",
		Project: "chum",
		Status:  "ready",
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}

	pending, err := e.ListPending(ctx, "chum")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	if len(pending) != 1 {
		t.Fatalf("expected 1 pending jarvis task, got %d", len(pending))
	}
	if pending[0].Status != "ready" {
		t.Errorf("status = %q, want %q", pending[0].Status, "ready")
	}
}

func TestSubmitWithPriority(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	id, err := e.Submit(context.Background(), WorkRequest{
		Title:    "High priority fix",
		Project:  "chum",
		Priority: 1,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	task, err := d.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Priority != 1 {
		t.Errorf("priority = %d, want 1", task.Priority)
	}
}

func TestTriggerDispatchWithoutTemporal(t *testing.T) {
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())

	err := e.TriggerDispatch(context.Background())
	if err == nil {
		t.Fatal("expected error when temporal client is nil")
	}
}

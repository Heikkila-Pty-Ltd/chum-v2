package engine

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"go.temporal.io/sdk/testsuite"

	_ "modernc.org/sqlite"
)

type spyBeadsStore struct {
	closeCalls  int
	updateCalls int
}

func (s *spyBeadsStore) List(_ context.Context, _ int) ([]beads.Issue, error)  { return nil, nil }
func (s *spyBeadsStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) { return nil, nil }
func (s *spyBeadsStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	return beads.Issue{ID: issueID, Status: "ready"}, nil
}
func (s *spyBeadsStore) Close(_ context.Context, _, _ string) error {
	s.closeCalls++
	return nil
}
func (s *spyBeadsStore) Create(_ context.Context, _ beads.CreateParams) (string, error) {
	return "", nil
}
func (s *spyBeadsStore) Update(_ context.Context, _ string, _ map[string]string) error {
	s.updateCalls++
	return nil
}
func (s *spyBeadsStore) Children(_ context.Context, _ string) ([]beads.Issue, error) { return nil, nil }
func (s *spyBeadsStore) AddDependency(_ context.Context, _, _ string) error          { return nil }

func newEngineBridgeTestDAG(t *testing.T) *dag.DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	d := dag.NewDAG(db)
	if err := d.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestCloseTaskWithDetailActivity_EnqueuesBridgeTerminalProjection(t *testing.T) {
	t.Parallel()
	d := newEngineBridgeTestDAG(t)
	ctx := context.Background()
	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "task-1",
		Title:   "Task",
		Project: "proj",
		Status:  "running",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "proj", "bd-1", "task-1", "fp"); err != nil {
		t.Fatalf("upsert mapping: %v", err)
	}

	spy := &spyBeadsStore{}
	a := &Activities{
		DAG:    d,
		Config: &config.Config{BeadsBridge: config.BeadsBridge{Enabled: true}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		BeadsClients: map[string]beads.Store{
			"proj": spy,
		},
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CloseTaskWithDetailActivity)
	_, err := env.ExecuteActivity(a.CloseTaskWithDetailActivity, "task-1", CloseDetail{
		Reason:    CloseCompleted,
		SubReason: "completed",
		PRNumber:  10,
		ReviewURL: "http://example.test/review",
	})
	if err != nil {
		t.Fatalf("close activity failed: %v", err)
	}

	row, err := d.GetBeadsOutboxRow(ctx, 1)
	if err != nil {
		t.Fatalf("get outbox row: %v", err)
	}
	if row.EventType != "task_terminal" {
		t.Fatalf("event_type=%q want task_terminal", row.EventType)
	}
	if spy.closeCalls != 0 || spy.updateCalls != 0 {
		t.Fatalf("bridge mode should enqueue outbox instead of direct writeback, closeCalls=%d updateCalls=%d", spy.closeCalls, spy.updateCalls)
	}
}

func TestRecordDispatchStartActivity_EnqueuesOnce(t *testing.T) {
	t.Parallel()
	d := newEngineBridgeTestDAG(t)
	ctx := context.Background()
	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "task-1",
		Title:   "Task",
		Project: "proj",
		Status:  "running",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "proj", "bd-1", "task-1", "fp"); err != nil {
		t.Fatalf("upsert mapping: %v", err)
	}
	da := &DispatchActivities{
		DAG:    d,
		Config: &config.Config{BeadsBridge: config.BeadsBridge{Enabled: true}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := da.RecordDispatchStartActivity(ctx, "task-1", "wf-1"); err != nil {
		t.Fatalf("record dispatch start: %v", err)
	}
	if err := da.RecordDispatchStartActivity(ctx, "task-1", "wf-2"); err != nil {
		t.Fatalf("record dispatch start replay: %v", err)
	}

	var count int
	if err := d.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM beads_sync_outbox WHERE event_type = 'task_started'").Scan(&count); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one start outbox event, got %d", count)
	}
}

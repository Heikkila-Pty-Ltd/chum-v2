package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
	"go.temporal.io/sdk/testsuite"

	_ "modernc.org/sqlite"
)

type spyBeadsStore struct {
	closeCalls  int
	updateCalls int
	createCalls int
	nextID      int
	created     []beads.CreateParams
	showIssues  map[string]beads.Issue
	showErr     error
}

func (s *spyBeadsStore) List(_ context.Context, _ int) ([]beads.Issue, error)  { return nil, nil }
func (s *spyBeadsStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) { return nil, nil }
func (s *spyBeadsStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	if s.showErr != nil {
		return beads.Issue{}, s.showErr
	}
	if s.showIssues != nil {
		if issue, ok := s.showIssues[issueID]; ok {
			return issue, nil
		}
		return beads.Issue{}, fmt.Errorf("issue %s not found", issueID)
	}
	return beads.Issue{ID: issueID, Status: "ready"}, nil
}
func (s *spyBeadsStore) Close(_ context.Context, _, _ string) error {
	s.closeCalls++
	return nil
}
func (s *spyBeadsStore) Create(_ context.Context, params beads.CreateParams) (string, error) {
	s.createCalls++
	s.created = append(s.created, params)
	if s.nextID == 0 {
		s.nextID = 1
	}
	id := fmt.Sprintf("bd-sub-%d", s.nextID)
	s.nextID++
	if s.showIssues == nil {
		s.showIssues = map[string]beads.Issue{}
	}
	s.showIssues[id] = beads.Issue{
		ID:                 id,
		Title:              params.Title,
		Description:        params.Description,
		Status:             "open",
		AcceptanceCriteria: params.Acceptance,
		EstimatedMinutes:   params.EstimatedMinutes,
	}
	return id, nil
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

func TestCloseTaskWithDetailActivity_PreservesExistingPRMetadata(t *testing.T) {
	t.Parallel()
	d := newEngineBridgeTestDAG(t)
	ctx := context.Background()

	prev := CloseDetail{
		Reason:    CloseNeedsReview,
		SubReason: "review_submit_failed",
		PRNumber:  39,
		ReviewURL: "https://example.test/pr/39",
	}
	prevRaw, err := json.Marshal(prev)
	if err != nil {
		t.Fatalf("marshal previous detail: %v", err)
	}

	if _, err := d.CreateTask(ctx, dag.Task{
		ID:       "task-pr-preserve",
		Title:    "Task",
		Project:  "proj",
		Status:   "running",
		ErrorLog: string(prevRaw),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	a := &Activities{
		DAG:    d,
		Config: &config.Config{BeadsBridge: config.BeadsBridge{Enabled: false}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CloseTaskWithDetailActivity)
	_, err = env.ExecuteActivity(a.CloseTaskWithDetailActivity, "task-pr-preserve", CloseDetail{
		Reason:    CloseNeedsReview,
		SubReason: "exec_failed",
	})
	if err != nil {
		t.Fatalf("close activity failed: %v", err)
	}

	gotTask, err := d.GetTask(ctx, "task-pr-preserve")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	var got CloseDetail
	if err := json.Unmarshal([]byte(gotTask.ErrorLog), &got); err != nil {
		t.Fatalf("parse stored error_log: %v", err)
	}
	if got.PRNumber != 39 {
		t.Fatalf("PRNumber = %d, want 39", got.PRNumber)
	}
	if got.ReviewURL != "https://example.test/pr/39" {
		t.Fatalf("ReviewURL = %q, want preserved URL", got.ReviewURL)
	}
}

func TestCloseTaskWithDetailActivity_BridgeModeSkipsUnmappedTaskWriteback(t *testing.T) {
	t.Parallel()
	d := newEngineBridgeTestDAG(t)
	ctx := context.Background()
	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "task-unmapped",
		Title:   "Task",
		Project: "proj",
		Status:  "running",
	}); err != nil {
		t.Fatalf("create task: %v", err)
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
	_, err := env.ExecuteActivity(a.CloseTaskWithDetailActivity, "task-unmapped", CloseDetail{
		Reason:    CloseCompleted,
		SubReason: "completed",
		PRNumber:  11,
	})
	if err != nil {
		t.Fatalf("close activity failed: %v", err)
	}

	var outboxCount int
	if err := d.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM beads_sync_outbox").Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("expected no outbox rows for unmapped task, got %d", outboxCount)
	}
	if spy.closeCalls != 0 || spy.updateCalls != 0 {
		t.Fatalf("expected no direct beads writeback for unmapped bridge task, closeCalls=%d updateCalls=%d", spy.closeCalls, spy.updateCalls)
	}
}

func TestCreateSubtasksActivity_CreatesBeadsBackedMappedSubtasks(t *testing.T) {
	t.Parallel()
	d := newEngineBridgeTestDAG(t)
	ctx := context.Background()
	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "task-parent",
		Title:   "Parent",
		Project: "proj",
		Status:  "ready",
	}); err != nil {
		t.Fatalf("create parent task: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "proj", "bd-parent", "task-parent", "fp"); err != nil {
		t.Fatalf("upsert parent mapping: %v", err)
	}

	spy := &spyBeadsStore{
		showIssues: map[string]beads.Issue{
			"bd-parent": {ID: "bd-parent", Status: "ready"},
		},
	}
	a := &Activities{
		DAG: d,
		Config: &config.Config{
			BeadsBridge: config.BeadsBridge{Enabled: true},
			Projects:    map[string]config.Project{},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		BeadsClients: map[string]beads.Store{
			"proj": spy,
		},
	}

	steps := []types.DecompStep{
		{Title: "Step one", Description: "Do first thing", Acceptance: "first ok", Estimate: 10},
		{Title: "Step two", Description: "Do second thing", Acceptance: "second ok", Estimate: 12},
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.CreateSubtasksActivity)
	var ids []string
	fut, err := env.ExecuteActivity(a.CreateSubtasksActivity, "task-parent", "proj", steps)
	if err != nil {
		t.Fatalf("create subtasks activity failed: %v", err)
	}
	if err := fut.Get(&ids); err != nil {
		t.Fatalf("read activity result: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 subtask IDs, got %d (%v)", len(ids), ids)
	}
	if ids[0] == ids[1] {
		t.Fatalf("expected unique subtask IDs, got %v", ids)
	}

	for _, id := range ids {
		task, err := d.GetTask(ctx, id)
		if err != nil {
			t.Fatalf("get subtask %s: %v", id, err)
		}
		if task.ParentID != "task-parent" {
			t.Fatalf("subtask %s parent = %q, want task-parent", id, task.ParentID)
		}
		mapping, err := d.GetBeadsMappingByTask(ctx, "proj", id)
		if err != nil {
			t.Fatalf("mapping for %s: %v", id, err)
		}
		if mapping.IssueID != id {
			t.Fatalf("mapping issue for %s = %q, want %q", id, mapping.IssueID, id)
		}
	}

	if spy.createCalls != 2 {
		t.Fatalf("expected 2 beads create calls, got %d", spy.createCalls)
	}
	if len(spy.created) != 2 {
		t.Fatalf("expected 2 create payloads, got %d", len(spy.created))
	}
	if spy.created[0].ParentID != "bd-parent" {
		t.Fatalf("first child parent issue = %q, want bd-parent", spy.created[0].ParentID)
	}
	if len(spy.created[1].Dependencies) != 1 || spy.created[1].Dependencies[0] != ids[0] {
		t.Fatalf("second child dependencies = %v, want [%s]", spy.created[1].Dependencies, ids[0])
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

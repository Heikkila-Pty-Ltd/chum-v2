package beadsbridge

import (
	"context"
	"fmt"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

type outboxStore struct {
	issues         map[string]beads.Issue
	updateFailures int
}

func (s *outboxStore) List(_ context.Context, _ int) ([]beads.Issue, error)  { return nil, nil }
func (s *outboxStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) { return nil, nil }
func (s *outboxStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	issue, ok := s.issues[issueID]
	if !ok {
		return beads.Issue{}, fmt.Errorf("not found")
	}
	return issue, nil
}
func (s *outboxStore) Close(_ context.Context, _, _ string) error                     { return nil }
func (s *outboxStore) Create(_ context.Context, _ beads.CreateParams) (string, error) { return "", nil }
func (s *outboxStore) Update(_ context.Context, issueID string, fields map[string]string) error {
	if s.updateFailures > 0 {
		s.updateFailures--
		return fmt.Errorf("transient update failure")
	}
	issue := s.issues[issueID]
	if v, ok := fields["status"]; ok {
		issue.Status = v
	}
	s.issues[issueID] = issue
	return nil
}
func (s *outboxStore) Children(_ context.Context, _ string) ([]beads.Issue, error) { return nil, nil }
func (s *outboxStore) AddDependency(_ context.Context, _, _ string) error          { return nil }

func TestOutboxWorker_DeliversStartProjection(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	ctx := context.Background()

	issueID := "bd-1"
	if err := enqueueEvent(ctx, d, "proj", issueID, "task-1", EventTaskStarted, "k1", map[string]any{
		"target_status": "in_progress",
	}); err != nil {
		t.Fatalf("enqueue start: %v", err)
	}

	client := &outboxStore{
		issues: map[string]beads.Issue{
			issueID: {ID: issueID, Status: "ready"},
		},
	}
	worker := &OutboxWorker{DAG: d, Logger: testLogger()}
	n, err := worker.ProcessProject(ctx, "proj", client, 10)
	if err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	if n != 1 {
		t.Fatalf("processed=%d want 1", n)
	}
	if got := client.issues[issueID].Status; got != "in_progress" {
		t.Fatalf("issue status=%q want in_progress", got)
	}
	row, err := d.GetBeadsOutboxRow(ctx, 1)
	if err != nil {
		t.Fatalf("get outbox row: %v", err)
	}
	if row.State != dag.BeadsOutboxStateDelivered {
		t.Fatalf("outbox state=%q want delivered", row.State)
	}
}

func TestOutboxWorker_RetryAndDeadLetter(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	ctx := context.Background()

	if _, err := d.EnqueueBeadsOutbox(ctx, dag.BeadsSyncOutboxRow{
		Project:        "proj",
		IssueID:        "bd-2",
		TaskID:         "task-2",
		EventType:      EventTaskStarted,
		Payload:        `{"target_status":"in_progress"}`,
		IdempotencyKey: "k2",
		MaxAttempts:    1,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	client := &outboxStore{
		issues: map[string]beads.Issue{
			"bd-2": {ID: "bd-2", Status: "ready"},
		},
		updateFailures: 5,
	}
	worker := &OutboxWorker{DAG: d, Logger: testLogger()}
	if _, err := worker.ProcessProject(ctx, "proj", client, 10); err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	row, err := d.GetBeadsOutboxRow(ctx, 1)
	if err != nil {
		t.Fatalf("get outbox row: %v", err)
	}
	if row.State != dag.BeadsOutboxStateDeadLetter {
		t.Fatalf("state=%q want dead_letter", row.State)
	}
}

func TestOutboxWorker_MonotonicGuardPreventsRegression(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	ctx := context.Background()

	if err := enqueueEvent(ctx, d, "proj", "bd-3", "task-3", EventTaskStarted, "k3", map[string]any{
		"target_status": "in_progress",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	client := &outboxStore{
		issues: map[string]beads.Issue{
			"bd-3": {ID: "bd-3", Status: "done"},
		},
	}
	worker := &OutboxWorker{DAG: d, Logger: testLogger()}
	if _, err := worker.ProcessProject(ctx, "proj", client, 10); err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	if got := client.issues["bd-3"].Status; got != "done" {
		t.Fatalf("status should not regress, got %q", got)
	}
}

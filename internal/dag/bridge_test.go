package dag

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestEnsureSchema_CreatesBridgeTables(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	tables := []string{
		"beads_sync_map",
		"beads_sync_outbox",
		"beads_sync_cursor",
		"beads_sync_audit",
	}
	for _, name := range tables {
		var got string
		err := d.DB().QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name = ?", name).Scan(&got)
		if err != nil {
			t.Fatalf("table %s missing: %v", name, err)
		}
	}
}

func TestBeadsMapping_UpsertRoundTrip(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, err := d.CreateTask(ctx, Task{ID: "task-1", Project: "p", Title: "t1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "p", "bd-1", "task-1", "fp-a"); err != nil {
		t.Fatalf("upsert mapping: %v", err)
	}
	row, err := d.GetBeadsMappingByIssue(ctx, "p", "bd-1")
	if err != nil {
		t.Fatalf("get mapping by issue: %v", err)
	}
	if row.TaskID != "task-1" || row.LastFingerprint != "fp-a" {
		t.Fatalf("unexpected mapping: %+v", row)
	}

	_, err = d.CreateTask(ctx, Task{ID: "task-2", Project: "p", Title: "t2"})
	if err != nil {
		t.Fatalf("create task2: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "p", "bd-1", "task-2", "fp-b"); err != nil {
		t.Fatalf("upsert mapping update: %v", err)
	}
	row, err = d.GetBeadsMappingByIssue(ctx, "p", "bd-1")
	if err != nil {
		t.Fatalf("get mapping by issue after update: %v", err)
	}
	if row.TaskID != "task-2" || row.LastFingerprint != "fp-b" {
		t.Fatalf("unexpected updated mapping: %+v", row)
	}
}

func TestBeadsCursor_RoundTrip(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	ts := time.Now().UTC().Add(-1 * time.Minute).Truncate(time.Second)
	if err := d.UpsertBeadsCursor(ctx, "p", "v1", ts); err != nil {
		t.Fatalf("upsert cursor: %v", err)
	}
	row, err := d.GetBeadsCursor(ctx, "p")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}
	if row.CursorValue != "v1" {
		t.Fatalf("cursor=%q want v1", row.CursorValue)
	}
}

func TestBeadsAudit_InsertList(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	if err := d.InsertBeadsAudit(ctx, BeadsSyncAuditRow{
		Project:     "p",
		IssueID:     "bd-1",
		EventKind:   "gate",
		Decision:    "skip",
		Reason:      "no_canary_label",
		Fingerprint: "fp",
		Details:     `{"x":1}`,
	}); err != nil {
		t.Fatalf("insert audit: %v", err)
	}
	rows, err := d.ListBeadsAudit(ctx, "p", 10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows)=%d want 1", len(rows))
	}
	if rows[0].Decision != "skip" {
		t.Fatalf("decision=%q want skip", rows[0].Decision)
	}
}

func TestBeadsOutbox_EnqueueClaimAndMark(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	id, err := d.EnqueueBeadsOutbox(ctx, BeadsSyncOutboxRow{
		Project:        "p",
		IssueID:        "bd-1",
		TaskID:         "task-1",
		EventType:      "task_started",
		Payload:        `{"status":"in_progress"}`,
		IdempotencyKey: "bd-1:start:abc",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	id2, err := d.EnqueueBeadsOutbox(ctx, BeadsSyncOutboxRow{
		Project:        "p",
		IssueID:        "bd-1",
		TaskID:         "task-1",
		EventType:      "task_started",
		Payload:        `{"status":"in_progress"}`,
		IdempotencyKey: "bd-1:start:abc",
	})
	if err != nil {
		t.Fatalf("enqueue duplicate idempotency key: %v", err)
	}
	if id2 != id {
		t.Fatalf("expected duplicate enqueue to return same row id, got %d vs %d", id2, id)
	}

	claimed, err := d.ClaimBeadsOutboxBatch(ctx, "p", 10, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed)=%d want 1", len(claimed))
	}
	if claimed[0].State != BeadsOutboxStateInflight {
		t.Fatalf("claimed state=%q want inflight", claimed[0].State)
	}

	if err := d.MarkBeadsOutboxRetry(ctx, claimed[0].ID, time.Now().UTC(), "transient"); err != nil {
		t.Fatalf("mark retry: %v", err)
	}
	row, err := d.GetBeadsOutboxRow(ctx, claimed[0].ID)
	if err != nil {
		t.Fatalf("get outbox row after retry: %v", err)
	}
	if row.State != BeadsOutboxStatePending || row.Attempts != 1 {
		t.Fatalf("unexpected row after retry: %+v", row)
	}

	claimed, err = d.ClaimBeadsOutboxBatch(ctx, "p", 10, time.Now().UTC().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("claim after retry: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed after retry)=%d want 1", len(claimed))
	}
	if err := d.MarkBeadsOutboxDelivered(ctx, claimed[0].ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	row, err = d.GetBeadsOutboxRow(ctx, claimed[0].ID)
	if err != nil {
		t.Fatalf("get outbox row after delivered: %v", err)
	}
	if row.State != BeadsOutboxStateDelivered {
		t.Fatalf("state=%q want delivered", row.State)
	}
}

func TestIsNoRows(t *testing.T) {
	t.Parallel()
	if !IsNoRows(sql.ErrNoRows) {
		t.Fatal("expected true for sql.ErrNoRows")
	}
	if IsNoRows(nil) {
		t.Fatal("expected false for nil error")
	}
}

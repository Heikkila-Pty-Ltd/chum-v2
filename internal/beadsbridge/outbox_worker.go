package beadsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

const (
	EventTaskStarted  = "task_started"
	EventTaskTerminal = "task_terminal"
)

// OutboxWorker delivers CHUM->beads bridge events from durable outbox rows.
type OutboxWorker struct {
	DAG    *dag.DAG
	Logger *slog.Logger
}

// ProcessProject delivers one batch of pending outbox events for a project.
func (w *OutboxWorker) ProcessProject(ctx context.Context, project string, client beads.Store, batchSize int) (int, error) {
	if w.DAG == nil {
		return 0, fmt.Errorf("outbox worker DAG is nil")
	}
	if client == nil {
		return 0, fmt.Errorf("outbox worker client is nil")
	}
	rows, err := w.DAG.ClaimBeadsOutboxBatch(ctx, project, batchSize, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("claim outbox batch: %w", err)
	}
	processed := 0
	for _, row := range rows {
		processed++
		delivery, deliverErr := w.deliverOne(ctx, client, row)
		if deliverErr == nil {
			if err := w.DAG.MarkBeadsOutboxDelivered(ctx, row.ID); err != nil {
				return processed, fmt.Errorf("mark delivered for %d: %w", row.ID, err)
			}
			_ = w.audit(ctx, row.Project, row.IssueID, row.TaskID, "outbox_delivery", "delivered", delivery.Reason, row.IdempotencyKey, map[string]any{
				"event_type":    row.EventType,
				"target_status": delivery.TargetStatus,
				"updated":       delivery.Updated,
			})
			continue
		}

		nextAttempts := row.Attempts + 1
		if nextAttempts >= row.MaxAttempts {
			if err := w.DAG.MarkBeadsOutboxDeadLetter(ctx, row.ID, deliverErr.Error()); err != nil {
				return processed, fmt.Errorf("mark dead-letter for %d: %w", row.ID, err)
			}
			_ = w.audit(ctx, row.Project, row.IssueID, row.TaskID, "outbox_delivery", "dead_letter", "max_attempts_exhausted", row.IdempotencyKey, map[string]any{
				"event_type": row.EventType,
				"error":      deliverErr.Error(),
				"attempts":   nextAttempts,
			})
			continue
		}

		backoff := retryBackoff(nextAttempts)
		nextAt := time.Now().UTC().Add(backoff)
		if err := w.DAG.MarkBeadsOutboxRetry(ctx, row.ID, nextAt, deliverErr.Error()); err != nil {
			return processed, fmt.Errorf("mark retry for %d: %w", row.ID, err)
		}
		_ = w.audit(ctx, row.Project, row.IssueID, row.TaskID, "outbox_delivery", "retry", "transient_delivery_failure", row.IdempotencyKey, map[string]any{
			"event_type":       row.EventType,
			"error":            deliverErr.Error(),
			"attempts":         nextAttempts,
			"next_attempt_sec": int(backoff.Seconds()),
		})
	}
	return processed, nil
}

// EnqueueTaskStarted enqueues one start-projection event.
func EnqueueTaskStarted(ctx context.Context, d *dag.DAG, project, issueID, taskID, workflowID string) error {
	idempotencyKey := fmt.Sprintf("%s:%s:start", issueID, taskID)
	payload := map[string]any{
		"task_id":       taskID,
		"workflow_id":   workflowID,
		"target_status": "in_progress",
		"event_at":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	return enqueueEvent(ctx, d, project, issueID, taskID, EventTaskStarted, idempotencyKey, payload)
}

// EnqueueTaskTerminal enqueues one terminal projection event.
func EnqueueTaskTerminal(ctx context.Context, d *dag.DAG, project, issueID, taskID, reason, subReason string, metadata map[string]any) error {
	target := "blocked"
	if strings.EqualFold(reason, "completed") {
		target = "done"
	}
	payload := map[string]any{
		"task_id":       taskID,
		"reason":        reason,
		"sub_reason":    subReason,
		"target_status": target,
		"event_at":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range metadata {
		payload[k] = v
	}
	idempotencyKey := fmt.Sprintf("%s:%s:terminal", issueID, taskID)
	return enqueueEvent(ctx, d, project, issueID, taskID, EventTaskTerminal, idempotencyKey, payload)
}

func enqueueEvent(ctx context.Context, d *dag.DAG, project, issueID, taskID, eventType, idempotencyKey string, payload map[string]any) error {
	if d == nil {
		return fmt.Errorf("enqueue event with nil DAG")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	if _, err := d.EnqueueBeadsOutbox(ctx, dag.BeadsSyncOutboxRow{
		Project:        project,
		IssueID:        issueID,
		TaskID:         taskID,
		EventType:      eventType,
		Payload:        string(b),
		IdempotencyKey: idempotencyKey,
	}); err != nil {
		return err
	}
	return d.InsertBeadsAudit(ctx, dag.BeadsSyncAuditRow{
		Project:     project,
		IssueID:     issueID,
		TaskID:      taskID,
		EventKind:   "outbox_enqueue",
		Decision:    "queued",
		Reason:      eventType,
		Fingerprint: idempotencyKey,
		Details:     string(b),
	})
}

type deliveryResult struct {
	Updated      bool
	Reason       string
	TargetStatus string
}

func (w *OutboxWorker) deliverOne(ctx context.Context, client beads.Store, row dag.BeadsSyncOutboxRow) (deliveryResult, error) {
	issue, err := client.Show(ctx, row.IssueID)
	if err != nil {
		return deliveryResult{}, fmt.Errorf("show issue %s: %w", row.IssueID, err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
		return deliveryResult{}, fmt.Errorf("parse payload for outbox row %d: %w", row.ID, err)
	}
	target := strings.ToLower(strings.TrimSpace(stringValue(payload["target_status"])))
	if target == "" {
		target = inferTargetStatus(row.EventType, payload)
	}
	if target == "" {
		return deliveryResult{}, fmt.Errorf("could not infer target status for event %s", row.EventType)
	}

	current := strings.ToLower(strings.TrimSpace(issue.Status))
	if !canTransitionMonotonic(current, target) {
		return deliveryResult{Updated: false, Reason: "monotonic_guard", TargetStatus: target}, nil
	}
	if current == target {
		return deliveryResult{Updated: false, Reason: "already_target_status", TargetStatus: target}, nil
	}
	if err := client.Update(ctx, row.IssueID, map[string]string{"status": target}); err != nil {
		return deliveryResult{}, fmt.Errorf("update issue %s to %s: %w", row.IssueID, target, err)
	}
	return deliveryResult{Updated: true, Reason: "updated", TargetStatus: target}, nil
}

func (w *OutboxWorker) audit(ctx context.Context, project, issueID, taskID, eventKind, decision, reason, fingerprint string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return w.DAG.InsertBeadsAudit(ctx, dag.BeadsSyncAuditRow{
		Project:     project,
		IssueID:     issueID,
		TaskID:      taskID,
		EventKind:   eventKind,
		Decision:    decision,
		Reason:      reason,
		Fingerprint: fingerprint,
		Details:     string(b),
	})
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	sec := math.Pow(2, float64(attempt))
	if sec > 300 {
		sec = 300
	}
	return time.Duration(sec) * time.Second
}

func inferTargetStatus(eventType string, payload map[string]any) string {
	switch eventType {
	case EventTaskStarted:
		return "in_progress"
	case EventTaskTerminal:
		if strings.EqualFold(stringValue(payload["reason"]), "completed") {
			return "done"
		}
		return "blocked"
	default:
		return ""
	}
}

func canTransitionMonotonic(current, target string) bool {
	return statusRank(target) >= statusRank(current)
}

func statusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "completed", "closed":
		return 3
	case "blocked":
		return 2
	case "in_progress", "running":
		return 1
	default:
		return 0
	}
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

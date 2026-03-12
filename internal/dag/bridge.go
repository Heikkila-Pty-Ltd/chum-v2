package dag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BeadsSyncMapRow maps a beads issue to a CHUM task.
type BeadsSyncMapRow struct {
	Project         string
	IssueID         string
	TaskID          string
	LastFingerprint string
	AdmittedAt      time.Time
	UpdatedAt       time.Time
}

// BeadsSyncCursorRow stores scanner checkpoint state per project.
type BeadsSyncCursorRow struct {
	Project     string
	CursorValue string
	LastScanAt  time.Time
	UpdatedAt   time.Time
}

// BeadsSyncAuditRow stores deterministic bridge decision history.
type BeadsSyncAuditRow struct {
	ID          int64
	Project     string
	IssueID     string
	TaskID      string
	EventKind   string
	Decision    string
	Reason      string
	Fingerprint string
	Details     string
	CreatedAt   time.Time
}

// BeadsSyncOutboxRow represents one outbound CHUM->beads delivery event.
type BeadsSyncOutboxRow struct {
	ID             int64
	Project        string
	IssueID        string
	TaskID         string
	EventType      string
	Payload        string
	IdempotencyKey string
	State          string
	Attempts       int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const (
	BeadsOutboxStatePending    = "pending"
	BeadsOutboxStateInflight   = "inflight"
	BeadsOutboxStateDelivered  = "delivered"
	BeadsOutboxStateDeadLetter = "dead_letter"
)

// UpsertBeadsMapping inserts or updates a beads->task mapping row.
func (d *DAG) UpsertBeadsMapping(ctx context.Context, project, issueID, taskID, fingerprint string) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO beads_sync_map(project, issue_id, task_id, last_fingerprint, admitted_at, updated_at)
		VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT(project, issue_id) DO UPDATE SET
			task_id = excluded.task_id,
			last_fingerprint = excluded.last_fingerprint,
			updated_at = datetime('now')
	`, project, issueID, taskID, fingerprint)
	if err != nil {
		return fmt.Errorf("upsert beads mapping %s/%s: %w", project, issueID, err)
	}
	return nil
}

// GetBeadsMappingByIssue returns a mapping row for one project/issue pair.
func (d *DAG) GetBeadsMappingByIssue(ctx context.Context, project, issueID string) (BeadsSyncMapRow, error) {
	var row BeadsSyncMapRow
	err := d.db.QueryRowContext(ctx, `
		SELECT project, issue_id, task_id, last_fingerprint, admitted_at, updated_at
		FROM beads_sync_map
		WHERE project = ? AND issue_id = ?
	`, project, issueID).Scan(
		&row.Project, &row.IssueID, &row.TaskID, &row.LastFingerprint, &row.AdmittedAt, &row.UpdatedAt,
	)
	if err != nil {
		return BeadsSyncMapRow{}, fmt.Errorf("get beads mapping by issue %s/%s: %w", project, issueID, err)
	}
	return row, nil
}

// GetBeadsMappingByTask returns a mapping row for a project/task pair.
func (d *DAG) GetBeadsMappingByTask(ctx context.Context, project, taskID string) (BeadsSyncMapRow, error) {
	var row BeadsSyncMapRow
	err := d.db.QueryRowContext(ctx, `
		SELECT project, issue_id, task_id, last_fingerprint, admitted_at, updated_at
		FROM beads_sync_map
		WHERE project = ? AND task_id = ?
	`, project, taskID).Scan(
		&row.Project, &row.IssueID, &row.TaskID, &row.LastFingerprint, &row.AdmittedAt, &row.UpdatedAt,
	)
	if err != nil {
		return BeadsSyncMapRow{}, fmt.Errorf("get beads mapping by task %s/%s: %w", project, taskID, err)
	}
	return row, nil
}

// ListBeadsMappings lists all beads mappings for a project.
func (d *DAG) ListBeadsMappings(ctx context.Context, project string) ([]BeadsSyncMapRow, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT project, issue_id, task_id, last_fingerprint, admitted_at, updated_at
		FROM beads_sync_map
		WHERE project = ?
		ORDER BY issue_id
	`, project)
	if err != nil {
		return nil, fmt.Errorf("list beads mappings for %s: %w", project, err)
	}
	defer rows.Close()
	var out []BeadsSyncMapRow
	for rows.Next() {
		var row BeadsSyncMapRow
		if err := rows.Scan(&row.Project, &row.IssueID, &row.TaskID, &row.LastFingerprint, &row.AdmittedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan beads mapping row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// UpsertBeadsCursor updates the incremental scanner cursor for a project.
func (d *DAG) UpsertBeadsCursor(ctx context.Context, project, cursorValue string, scanAt time.Time) error {
	if scanAt.IsZero() {
		scanAt = time.Now().UTC()
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO beads_sync_cursor(project, cursor_value, last_scan_at, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(project) DO UPDATE SET
			cursor_value = excluded.cursor_value,
			last_scan_at = excluded.last_scan_at,
			updated_at = datetime('now')
	`, project, cursorValue, scanAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert beads cursor for %s: %w", project, err)
	}
	return nil
}

// GetBeadsCursor returns cursor state for a project.
func (d *DAG) GetBeadsCursor(ctx context.Context, project string) (BeadsSyncCursorRow, error) {
	var row BeadsSyncCursorRow
	err := d.db.QueryRowContext(ctx, `
		SELECT project, cursor_value, last_scan_at, updated_at
		FROM beads_sync_cursor
		WHERE project = ?
	`, project).Scan(&row.Project, &row.CursorValue, &row.LastScanAt, &row.UpdatedAt)
	if err != nil {
		return BeadsSyncCursorRow{}, fmt.Errorf("get beads cursor for %s: %w", project, err)
	}
	return row, nil
}

// InsertBeadsAudit inserts one audit trail row.
func (d *DAG) InsertBeadsAudit(ctx context.Context, row BeadsSyncAuditRow) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO beads_sync_audit(project, issue_id, task_id, event_kind, decision, reason, fingerprint, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, row.Project, row.IssueID, row.TaskID, row.EventKind, row.Decision, row.Reason, row.Fingerprint, row.Details)
	if err != nil {
		return fmt.Errorf("insert beads audit: %w", err)
	}
	return nil
}

// ListBeadsAudit returns recent bridge audit events for one project.
func (d *DAG) ListBeadsAudit(ctx context.Context, project string, limit int) ([]BeadsSyncAuditRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, project, issue_id, task_id, event_kind, decision, reason, fingerprint, details, created_at
		FROM beads_sync_audit
		WHERE project = ?
		ORDER BY id DESC
		LIMIT ?
	`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("list beads audit for %s: %w", project, err)
	}
	defer rows.Close()
	var out []BeadsSyncAuditRow
	for rows.Next() {
		var row BeadsSyncAuditRow
		if err := rows.Scan(&row.ID, &row.Project, &row.IssueID, &row.TaskID, &row.EventKind, &row.Decision, &row.Reason, &row.Fingerprint, &row.Details, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan beads audit row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// EnqueueBeadsOutbox inserts a pending outbox row using idempotency key dedupe.
func (d *DAG) EnqueueBeadsOutbox(ctx context.Context, row BeadsSyncOutboxRow) (int64, error) {
	if row.MaxAttempts <= 0 {
		row.MaxAttempts = 5
	}
	if row.State == "" {
		row.State = BeadsOutboxStatePending
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO beads_sync_outbox(
			project, issue_id, task_id, event_type, payload, idempotency_key, state, attempts, max_attempts, next_attempt_at, last_error
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, datetime('now'), '')
	`, row.Project, row.IssueID, row.TaskID, row.EventType, row.Payload, row.IdempotencyKey, row.State, row.MaxAttempts)
	if err != nil {
		return 0, fmt.Errorf("enqueue beads outbox event: %w", err)
	}
	var id int64
	err = d.db.QueryRowContext(ctx, `
		SELECT id FROM beads_sync_outbox
		WHERE project = ? AND idempotency_key = ?
	`, row.Project, row.IdempotencyKey).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("resolve outbox id: %w", err)
	}
	return id, nil
}

// ClaimBeadsOutboxBatch claims pending rows and marks them inflight.
func (d *DAG) ClaimBeadsOutboxBatch(ctx context.Context, project string, limit int, now time.Time) ([]BeadsSyncOutboxRow, error) {
	if limit <= 0 {
		limit = 25
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin outbox claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM beads_sync_outbox
		WHERE project = ?
		  AND state = ?
		  AND next_attempt_at <= ?
		ORDER BY id ASC
		LIMIT ?
	`, project, BeadsOutboxStatePending, now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("select pending outbox rows: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan pending outbox id: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending outbox ids: %w", err)
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty outbox claim: %w", err)
		}
		return nil, nil
	}

	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE beads_sync_outbox
			SET state = ?, updated_at = datetime('now')
			WHERE id = ? AND state = ?
		`, BeadsOutboxStateInflight, id, BeadsOutboxStatePending); err != nil {
			return nil, fmt.Errorf("claim outbox row %d: %w", id, err)
		}
	}

	claimed := make([]BeadsSyncOutboxRow, 0, len(ids))
	for _, id := range ids {
		var row BeadsSyncOutboxRow
		err := tx.QueryRowContext(ctx, `
			SELECT id, project, issue_id, task_id, event_type, payload, idempotency_key, state, attempts, max_attempts,
			       next_attempt_at, last_error, created_at, updated_at
			FROM beads_sync_outbox
			WHERE id = ?
		`, id).Scan(
			&row.ID, &row.Project, &row.IssueID, &row.TaskID, &row.EventType, &row.Payload, &row.IdempotencyKey,
			&row.State, &row.Attempts, &row.MaxAttempts, &row.NextAttemptAt, &row.LastError, &row.CreatedAt, &row.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("load claimed outbox row %d: %w", id, err)
		}
		claimed = append(claimed, row)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbox claim tx: %w", err)
	}
	return claimed, nil
}

// MarkBeadsOutboxDelivered marks an inflight event as delivered.
func (d *DAG) MarkBeadsOutboxDelivered(ctx context.Context, id int64) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE beads_sync_outbox
		SET state = ?, updated_at = datetime('now')
		WHERE id = ?
	`, BeadsOutboxStateDelivered, id)
	if err != nil {
		return fmt.Errorf("mark outbox delivered %d: %w", id, err)
	}
	return nil
}

// MarkBeadsOutboxRetry marks delivery failure and schedules another attempt.
func (d *DAG) MarkBeadsOutboxRetry(ctx context.Context, id int64, nextAttemptAt time.Time, lastErr string) error {
	if nextAttemptAt.IsZero() {
		nextAttemptAt = time.Now().UTC().Add(10 * time.Second)
	}
	_, err := d.db.ExecContext(ctx, `
		UPDATE beads_sync_outbox
		SET attempts = attempts + 1,
		    state = ?,
		    next_attempt_at = ?,
		    last_error = ?,
		    updated_at = datetime('now')
		WHERE id = ?
	`, BeadsOutboxStatePending, nextAttemptAt.UTC().Format(time.RFC3339Nano), lastErr, id)
	if err != nil {
		return fmt.Errorf("mark outbox retry %d: %w", id, err)
	}
	return nil
}

// MarkBeadsOutboxDeadLetter marks a row as permanently failed.
func (d *DAG) MarkBeadsOutboxDeadLetter(ctx context.Context, id int64, lastErr string) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE beads_sync_outbox
		SET attempts = attempts + 1,
		    state = ?,
		    last_error = ?,
		    updated_at = datetime('now')
		WHERE id = ?
	`, BeadsOutboxStateDeadLetter, lastErr, id)
	if err != nil {
		return fmt.Errorf("mark outbox dead-letter %d: %w", id, err)
	}
	return nil
}

// GetBeadsOutboxRow retrieves one outbox row by ID.
func (d *DAG) GetBeadsOutboxRow(ctx context.Context, id int64) (BeadsSyncOutboxRow, error) {
	var row BeadsSyncOutboxRow
	err := d.db.QueryRowContext(ctx, `
		SELECT id, project, issue_id, task_id, event_type, payload, idempotency_key, state, attempts, max_attempts,
		       next_attempt_at, last_error, created_at, updated_at
		FROM beads_sync_outbox
		WHERE id = ?
	`, id).Scan(
		&row.ID, &row.Project, &row.IssueID, &row.TaskID, &row.EventType, &row.Payload, &row.IdempotencyKey,
		&row.State, &row.Attempts, &row.MaxAttempts, &row.NextAttemptAt, &row.LastError, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return BeadsSyncOutboxRow{}, fmt.Errorf("get outbox row %d: %w", id, err)
	}
	return row, nil
}

// GetBeadsMappingsByTasks returns beads mappings for multiple task IDs in a single query.
func (d *DAG) GetBeadsMappingsByTasks(ctx context.Context, project string, taskIDs []string) (map[string]BeadsSyncMapRow, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	query := `SELECT project, issue_id, task_id, last_fingerprint, admitted_at, updated_at
		FROM beads_sync_map WHERE project = ? AND task_id IN (` + sqlPlaceholders(len(taskIDs)) + `)`
	args := make([]any, 0, len(taskIDs)+1)
	args = append(args, project)
	for _, id := range taskIDs {
		args = append(args, id)
	}
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get beads mappings by tasks: %w", err)
	}
	defer rows.Close()
	m := make(map[string]BeadsSyncMapRow)
	for rows.Next() {
		var row BeadsSyncMapRow
		if err := rows.Scan(&row.Project, &row.IssueID, &row.TaskID, &row.LastFingerprint, &row.AdmittedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan beads mapping row: %w", err)
		}
		m[row.TaskID] = row
	}
	return m, rows.Err()
}

// IsNoRows returns whether an error chain contains sql.ErrNoRows.
func IsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

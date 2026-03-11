package dag

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// PlanningSnapshot is a DAG-facing alias for the shared planning artifact.
type PlanningSnapshot = types.PlanningSnapshot

// UpsertPlanningSnapshot stores the latest snapshot for a planning session.
func (d *DAG) UpsertPlanningSnapshot(ctx context.Context, snapshot PlanningSnapshot) error {
	if snapshot.SessionID == "" {
		return fmt.Errorf("upsert planning snapshot: session_id is required")
	}
	if snapshot.GoalID == "" {
		return fmt.Errorf("upsert planning snapshot: goal_id is required")
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal planning snapshot: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO planning_snapshots (
			session_id, task_id, project, phase, status, snapshot_json
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			task_id = excluded.task_id,
			project = excluded.project,
			phase = excluded.phase,
			status = excluded.status,
			snapshot_json = excluded.snapshot_json,
			updated_at = datetime('now')`,
		snapshot.SessionID,
		snapshot.GoalID,
		snapshot.Project,
		snapshot.Phase,
		snapshot.Status,
		string(payload),
	)
	if err != nil {
		return fmt.Errorf("upsert planning snapshot: %w", err)
	}
	return nil
}

// GetPlanningSnapshot fetches a single planning session snapshot.
func (d *DAG) GetPlanningSnapshot(ctx context.Context, sessionID string) (PlanningSnapshot, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT snapshot_json, created_at, updated_at
		FROM planning_snapshots
		WHERE session_id = ?`, sessionID)
	return scanPlanningSnapshot(row)
}

// GetLatestPlanningSnapshotForTask returns the newest planning session for a task.
func (d *DAG) GetLatestPlanningSnapshotForTask(ctx context.Context, taskID string) (PlanningSnapshot, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT snapshot_json, created_at, updated_at
		FROM planning_snapshots
		WHERE task_id = ?
		ORDER BY updated_at DESC, created_at DESC, rowid DESC
		LIMIT 1`, taskID)
	return scanPlanningSnapshot(row)
}

// ListPlanningSnapshotsForTask lists planning sessions for a task newest first.
func (d *DAG) ListPlanningSnapshotsForTask(ctx context.Context, taskID string) ([]PlanningSnapshot, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT snapshot_json, created_at, updated_at
		FROM planning_snapshots
		WHERE task_id = ?
		ORDER BY updated_at DESC, created_at DESC, rowid DESC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list planning snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []PlanningSnapshot
	for rows.Next() {
		snapshot, err := scanPlanningSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

type planningSnapshotRow interface {
	Scan(dest ...any) error
}

func scanPlanningSnapshot(row planningSnapshotRow) (PlanningSnapshot, error) {
	var raw string
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&raw, &createdAt, &updatedAt); err != nil {
		return PlanningSnapshot{}, err
	}

	var snapshot PlanningSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return PlanningSnapshot{}, fmt.Errorf("unmarshal planning snapshot: %w", err)
	}
	snapshot.CreatedAt = createdAt
	snapshot.UpdatedAt = updatedAt
	return snapshot, nil
}

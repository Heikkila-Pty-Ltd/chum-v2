package dag

import (
	"context"
	"fmt"
	"strings"
)

// SetTaskTargets replaces all targets for a task.
func (d *DAG) SetTaskTargets(ctx context.Context, taskID string, targets []TaskTarget) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, "DELETE FROM task_targets WHERE task_id = ?", taskID); err != nil {
		return fmt.Errorf("clear targets: %w", err)
	}
	for _, t := range targets {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO task_targets (task_id, file_path, symbol_name, symbol_kind) VALUES (?, ?, ?, ?)",
			taskID, t.FilePath, t.SymbolName, t.SymbolKind); err != nil {
			return fmt.Errorf("insert target: %w", err)
		}
	}
	return tx.Commit()
}

// GetTaskTargets returns resolved targets for a single task.
func (d *DAG) GetTaskTargets(ctx context.Context, taskID string) ([]TaskTarget, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT task_id, file_path, symbol_name, symbol_kind FROM task_targets WHERE task_id = ?",
		taskID)
	if err != nil {
		return nil, fmt.Errorf("get targets: %w", err)
	}
	defer rows.Close()
	var targets []TaskTarget
	for rows.Next() {
		var t TaskTarget
		if err := rows.Scan(&t.TaskID, &t.FilePath, &t.SymbolName, &t.SymbolKind); err != nil {
			return nil, fmt.Errorf("scan target: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// GetAllTargetsForStatuses returns taskID → targets for all tasks in the given statuses.
func (d *DAG) GetAllTargetsForStatuses(ctx context.Context, project string, statuses ...string) (map[string][]TaskTarget, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(statuses))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf(`SELECT tt.task_id, tt.file_path, tt.symbol_name, tt.symbol_kind
		FROM task_targets tt
		JOIN tasks t ON t.id = tt.task_id
		WHERE t.project = ? AND t.status IN (%s)`, placeholders)
	args := []any{project}
	for _, s := range statuses {
		args = append(args, s)
	}
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get all targets: %w", err)
	}
	defer rows.Close()
	result := make(map[string][]TaskTarget)
	for rows.Next() {
		var t TaskTarget
		if err := rows.Scan(&t.TaskID, &t.FilePath, &t.SymbolName, &t.SymbolKind); err != nil {
			return nil, fmt.Errorf("scan target: %w", err)
		}
		result[t.TaskID] = append(result[t.TaskID], t)
	}
	return result, rows.Err()
}

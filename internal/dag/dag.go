package dag

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
	_ "modernc.org/sqlite"
)

// DAG is a SQLite-backed directed acyclic graph of tasks.
type DAG struct {
	db *sql.DB
}

// NewDAG creates a DAG wrapping an existing database connection.
func NewDAG(db *sql.DB) *DAG {
	return &DAG{db: db}
}

// Open creates a new DAG with a SQLite connection to the given path.
func Open(dbPath string) (*DAG, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}
	d := &DAG{db: db}
	if err := d.EnsureSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the underlying database connection.
func (d *DAG) Close() error { return d.db.Close() }

// DB returns the underlying database connection for direct queries.
func (d *DAG) DB() *sql.DB { return d.db }

func generateTaskID(project string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(99999))
	if err != nil {
		return "", err
	}
	prefix := "chum"
	if project != "" {
		parts := strings.SplitN(project, "-", 2)
		if len(parts[0]) >= 2 {
			prefix = strings.ToLower(parts[0][:2])
		}
	}
	return fmt.Sprintf("%s-%05d", prefix, n.Int64()), nil
}

// CreateTask inserts a new task and returns its generated ID.
func (d *DAG) CreateTask(ctx context.Context, t Task) (string, error) {
	if t.ID == "" {
		id, err := generateTaskID(t.Project)
		if err != nil {
			return "", fmt.Errorf("generate id: %w", err)
		}
		t.ID = id
	}
	labelsJSON := []byte("[]")
	if t.Labels != nil {
		var marshalErr error
		labelsJSON, marshalErr = json.Marshal(t.Labels)
		if marshalErr != nil {
			return "", fmt.Errorf("marshal labels: %w", marshalErr)
		}
	}
	metadataJSON := []byte("{}")
	if t.Metadata != nil {
		var marshalErr error
		metadataJSON, marshalErr = json.Marshal(t.Metadata)
		if marshalErr != nil {
			return "", fmt.Errorf("marshal metadata: %w", marshalErr)
		}
	}
	status := t.Status
	if status == "" {
		status = string(types.StatusOpen)
	}
	taskType := t.Type
	if taskType == "" {
		taskType = "task"
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO tasks
		(id, title, description, status, priority, type, assignee, labels,
		 estimate_minutes, parent_id, acceptance, project, error_log, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, status, t.Priority, taskType,
		t.Assignee, string(labelsJSON), t.EstimateMinutes,
		t.ParentID, t.Acceptance, t.Project, t.ErrorLog, string(metadataJSON),
	)
	if err != nil {
		return "", fmt.Errorf("insert task: %w", err)
	}
	return t.ID, nil
}

// CreateSubtasksAtomic creates subtasks, wires sequential dependencies, rewires
// parent dependents to the last subtask, and marks the parent as "decomposed" —
// all in a single transaction. If any step fails, everything is rolled back.
func (d *DAG) CreateSubtasksAtomic(ctx context.Context, parentID string, tasks []Task) ([]string, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var ids []string
	for _, t := range tasks {
		if t.ID == "" {
			id, err := generateTaskID(t.Project)
			if err != nil {
				return nil, fmt.Errorf("generate id: %w", err)
			}
			t.ID = id
		}
		labelsJSON := []byte("[]")
		if t.Labels != nil {
			var marshalErr error
			labelsJSON, marshalErr = json.Marshal(t.Labels)
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal labels for %q: %w", t.Title, marshalErr)
			}
		}
		metadataJSON := []byte("{}")
		if t.Metadata != nil {
			var marshalErr error
			metadataJSON, marshalErr = json.Marshal(t.Metadata)
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal metadata for %q: %w", t.Title, marshalErr)
			}
		}
		status := t.Status
		if status == "" {
			status = string(types.StatusOpen)
		}
		taskType := t.Type
		if taskType == "" {
			taskType = "task"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks
			(id, title, description, status, priority, type, assignee, labels,
			 estimate_minutes, parent_id, acceptance, project, error_log, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.Title, t.Description, status, t.Priority, taskType,
			t.Assignee, string(labelsJSON), t.EstimateMinutes,
			t.ParentID, t.Acceptance, t.Project, t.ErrorLog, string(metadataJSON),
		); err != nil {
			return nil, fmt.Errorf("insert subtask %q: %w", t.Title, err)
		}
		ids = append(ids, t.ID)
	}

	// Wire sequential dependencies: step[i+1] depends on step[i]
	for i := 1; i < len(ids); i++ {
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO task_edges (from_task, to_task, source) VALUES (?, ?, 'beads')",
			ids[i], ids[i-1]); err != nil {
			return nil, fmt.Errorf("add subtask edge %s→%s: %w", ids[i], ids[i-1], err)
		}
	}

	// Inherit: parent's own prerequisites (to_task entries where from_task = parent)
	// become prerequisites of the first subtask, so S1 won't run before them.
	// Preserves the original edge source. Deletes the parent's edges after copying.
	firstSubtask := ids[0]
	prereqRows, err := tx.QueryContext(ctx,
		"SELECT to_task, source FROM task_edges WHERE from_task = ?", parentID)
	if err != nil {
		return nil, fmt.Errorf("get parent prerequisites: %w", err)
	}
	type edgeInfo struct {
		target, source string
	}
	var prereqs []edgeInfo
	for prereqRows.Next() {
		var ei edgeInfo
		if err := prereqRows.Scan(&ei.target, &ei.source); err != nil {
			prereqRows.Close()
			return nil, fmt.Errorf("scan parent prerequisite: %w", err)
		}
		prereqs = append(prereqs, ei)
	}
	prereqRows.Close()
	if err := prereqRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate parent prerequisites: %w", err)
	}

	for _, prereq := range prereqs {
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO task_edges (from_task, to_task, source) VALUES (?, ?, ?)",
			firstSubtask, prereq.target, prereq.source); err != nil {
			return nil, fmt.Errorf("inherit prereq edge %s→%s: %w", firstSubtask, prereq.target, err)
		}
	}
	// Clean up parent's upstream edges
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM task_edges WHERE from_task = ?", parentID); err != nil {
		return nil, fmt.Errorf("remove parent upstream edges: %w", err)
	}

	// Rewire: dependents of parent now depend on the last subtask.
	// Preserves the original edge source.
	lastSubtask := ids[len(ids)-1]
	rows, err := tx.QueryContext(ctx,
		"SELECT from_task, source FROM task_edges WHERE to_task = ?", parentID)
	if err != nil {
		return nil, fmt.Errorf("get parent dependents: %w", err)
	}
	var dependents []edgeInfo
	for rows.Next() {
		var ei edgeInfo
		if err := rows.Scan(&ei.target, &ei.source); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan parent dependent: %w", err)
		}
		dependents = append(dependents, ei)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate parent dependents: %w", err)
	}

	for _, dep := range dependents {
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO task_edges (from_task, to_task, source) VALUES (?, ?, ?)",
			dep.target, lastSubtask, dep.source); err != nil {
			return nil, fmt.Errorf("rewire edge %s→%s: %w", dep.target, lastSubtask, err)
		}
	}
	// Clean up parent's downstream edges
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM task_edges WHERE to_task = ?", parentID); err != nil {
		return nil, fmt.Errorf("remove parent downstream edges: %w", err)
	}

	// Mark parent as decomposed
	if _, err := tx.ExecContext(ctx,
		"UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		types.StatusDecomposed, parentID); err != nil {
		return nil, fmt.Errorf("mark parent decomposed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return ids, nil
}

// GetTask retrieves a task by ID.
func (d *DAG) GetTask(ctx context.Context, id string) (Task, error) {
	row := d.db.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE id = ?", id)
	return scanTask(row)
}

// CountTasksByStatus returns the total number of tasks with any of the given
// statuses across all projects. Used by the shutdown drain loop to ensure no
// in-flight work is missed regardless of config changes.
func (d *DAG) CountTasksByStatus(ctx context.Context, statuses ...string) (int, error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	query := "SELECT COUNT(*) FROM tasks WHERE status IN (" + sqlPlaceholders(len(statuses)) + ")"
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = s
	}
	var count int
	if err := d.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count tasks by status: %w", err)
	}
	return count, nil
}

// ListTasks returns tasks for a project, optionally filtering by statuses.
func (d *DAG) ListTasks(ctx context.Context, project string, statuses ...string) ([]Task, error) {
	query := "SELECT " + taskColumns + " FROM tasks WHERE project = ?"
	args := []any{project}
	if len(statuses) > 0 {
		query += " AND status IN (" + sqlPlaceholders(len(statuses)) + ")"
		for _, s := range statuses {
			args = append(args, s)
		}
	}
	query += " ORDER BY priority ASC, created_at ASC"
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// UpdateTask updates specific fields on a task.
func (d *DAG) UpdateTask(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	allowed := map[string]bool{
		"title": true, "description": true, "status": true, "priority": true,
		"type": true, "assignee": true, "labels": true, "estimate_minutes": true,
		"parent_id": true, "acceptance": true, "project": true, "error_log": true,
		"metadata": true, "actual_duration_sec": true, "iterations_used": true,
		"attempt_count": true,
	}
	for k, v := range fields {
		if !allowed[k] {
			return fmt.Errorf("field %q is not updatable", k)
		}
		if k == "labels" {
			if labels, ok := v.([]string); ok {
				b, err := json.Marshal(labels)
				if err != nil {
					return fmt.Errorf("marshal labels: %w", err)
				}
				v = string(b)
			}
		}
		if k == "metadata" {
			if meta, ok := v.(map[string]string); ok {
				b, err := json.Marshal(meta)
				if err != nil {
					return fmt.Errorf("marshal metadata: %w", err)
				}
				v = string(b)
			}
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", k))
		args = append(args, v)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	res, err := execWithBusyRetry(ctx, func() (sql.Result, error) {
		return d.db.ExecContext(ctx, query, args...)
	})
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task %q not found", id)
	}
	return nil
}

// CloseTask sets a task's status (e.g. "completed", "dod_failed", "plan_failed").
func (d *DAG) CloseTask(ctx context.Context, id, status string) error {
	return d.UpdateTask(ctx, id, map[string]any{"status": status})
}

// UpdateTaskStatus sets a task's status (alias for CloseTask with clearer naming).
func (d *DAG) UpdateTaskStatus(ctx context.Context, id, status string) error {
	return d.UpdateTask(ctx, id, map[string]any{"status": status})
}

// GetReadyNodes returns tasks with status="ready" whose dependencies are satisfied.
// Dependency semantics are source-aware:
// - non-AST edges require terminal deps (completed/done)
// - AST fences only block while the dependency is still active (ready/running)
// Legacy "done" remains satisfied for backward compatibility.
func (d *DAG) GetReadyNodes(ctx context.Context, project string) ([]Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks t
		WHERE t.project = ? AND t.status = ?
		AND NOT EXISTS (
			SELECT 1 FROM task_edges e
			LEFT JOIN tasks dep ON dep.id = e.to_task
			WHERE e.from_task = t.id
			AND (
				dep.id IS NULL OR (
					CASE
						WHEN lower(trim(COALESCE(e.source, ''))) = 'ast'
							THEN lower(trim(COALESCE(dep.status, ''))) IN (?, ?)
						ELSE
							lower(trim(COALESCE(dep.status, ''))) NOT IN (?, ?)
					END
				)
			)
		)
		ORDER BY t.priority ASC, t.created_at ASC`
	rows, err := d.db.QueryContext(ctx, query,
		project,
		types.StatusReady,
		types.StatusReady,
		types.StatusRunning,
		types.StatusCompleted,
		types.StatusDone,
	)
	if err != nil {
		return nil, fmt.Errorf("get ready nodes: %w", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// GetApprovedNodes returns tasks with status="approved" whose dependencies are satisfied.
// Same dependency semantics as GetReadyNodes but for the approved dispatch gate.
func (d *DAG) GetApprovedNodes(ctx context.Context, project string) ([]Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks t
		WHERE t.project = ? AND t.status = ?
		AND NOT EXISTS (
			SELECT 1 FROM task_edges e
			LEFT JOIN tasks dep ON dep.id = e.to_task
			WHERE e.from_task = t.id
			AND (
				dep.id IS NULL OR (
					CASE
						WHEN lower(trim(COALESCE(e.source, ''))) = 'ast'
							THEN lower(trim(COALESCE(dep.status, ''))) IN (?, ?)
						ELSE
							lower(trim(COALESCE(dep.status, ''))) NOT IN (?, ?)
					END
				)
			)
		)
		ORDER BY t.priority ASC, t.created_at ASC`
	rows, err := d.db.QueryContext(ctx, query,
		project,
		types.StatusApproved,
		types.StatusReady,
		types.StatusRunning,
		types.StatusCompleted,
		types.StatusDone,
	)
	if err != nil {
		return nil, fmt.Errorf("get approved nodes: %w", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		t, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// CountChildrenByParent returns a map from parent_id to child count for a project.
func (d *DAG) CountChildrenByParent(ctx context.Context, project string) (map[string]int, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT parent_id, COUNT(*) FROM tasks WHERE project = ? AND parent_id != '' GROUP BY parent_id`, project)
	if err != nil {
		return nil, fmt.Errorf("count children: %w", err)
	}
	defer rows.Close()
	m := make(map[string]int)
	for rows.Next() {
		var pid string
		var cnt int
		if err := rows.Scan(&pid, &cnt); err != nil {
			return nil, err
		}
		m[pid] = cnt
	}
	return m, rows.Err()
}

// PauseProjectTasks sets all running/ready/approved tasks in a project to "paused".
// Ready and approved tasks remember their prior state in metadata so resume can
// restore the approval gate correctly. Returns the number of affected rows.
func (d *DAG) PauseProjectTasks(ctx context.Context, project string) (int64, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin pause project tasks tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks
		 WHERE project = ? AND status IN ('running', 'ready', 'approved')`,
		project,
	)
	if err != nil {
		return 0, fmt.Errorf("query pause project tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		task, scanErr := scanTaskRows(rows)
		if scanErr != nil {
			return 0, fmt.Errorf("scan pause project task: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate pause project tasks: %w", err)
	}
	if len(tasks) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE tasks SET status = 'paused', metadata = ?, updated_at = datetime('now') WHERE id = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare pause project task stmt: %w", err)
	}
	defer stmt.Close()

	var affected int64
	for _, task := range tasks {
		meta := cloneMetadata(task.Metadata)
		switch task.Status {
		case string(types.StatusReady), string(types.StatusApproved):
			meta["paused_from_status"] = task.Status
		default:
			delete(meta, "paused_from_status")
		}
		metaJSON, err := json.Marshal(meta)
		if err != nil {
			return 0, fmt.Errorf("marshal pause metadata for %s: %w", task.ID, err)
		}
		res, err := stmt.ExecContext(ctx, string(metaJSON), task.ID)
		if err != nil {
			return 0, fmt.Errorf("pause task %s: %w", task.ID, err)
		}
		n, _ := res.RowsAffected()
		affected += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit pause project tasks: %w", err)
	}
	return affected, nil
}

// ResumeProjectTasks sets paused tasks back to their prior dispatch state.
// Tasks paused from ready or approved are restored to that state; other paused
// tasks fall back to ready.
func (d *DAG) ResumeProjectTasks(ctx context.Context, project string) (int64, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin resume project tasks tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE project = ? AND status = 'paused'`,
		project,
	)
	if err != nil {
		return 0, fmt.Errorf("query resume project tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		task, scanErr := scanTaskRows(rows)
		if scanErr != nil {
			return 0, fmt.Errorf("scan resume project task: %w", scanErr)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate resume project tasks: %w", err)
	}
	if len(tasks) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE tasks SET status = ?, metadata = ?, updated_at = datetime('now') WHERE id = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare resume project task stmt: %w", err)
	}
	defer stmt.Close()

	var affected int64
	for _, task := range tasks {
		meta := cloneMetadata(task.Metadata)
		resumeStatus := string(types.StatusReady)
		switch meta["paused_from_status"] {
		case string(types.StatusReady), string(types.StatusApproved):
			resumeStatus = meta["paused_from_status"]
		}
		delete(meta, "paused_from_status")
		metaJSON, err := json.Marshal(meta)
		if err != nil {
			return 0, fmt.Errorf("marshal resume metadata for %s: %w", task.ID, err)
		}
		res, err := stmt.ExecContext(ctx, resumeStatus, string(metaJSON), task.ID)
		if err != nil {
			return 0, fmt.Errorf("resume task %s: %w", task.ID, err)
		}
		n, _ := res.RowsAffected()
		affected += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit resume project tasks: %w", err)
	}
	return affected, nil
}

// ReorderTaskPriorities updates the priority field of the given tasks to match
// their position in the slice (0 = highest priority). All tasks must exist and
// be in "ready" or "approved" status. Uses a transaction for atomicity.
func (d *DAG) ReorderTaskPriorities(ctx context.Context, taskIDs []string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE tasks SET priority = ?, updated_at = datetime('now') WHERE id = ? AND status IN ('ready', 'approved')`)
	if err != nil {
		return fmt.Errorf("prepare reorder stmt: %w", err)
	}
	defer stmt.Close()
	for i, id := range taskIDs {
		res, err := stmt.ExecContext(ctx, i, id)
		if err != nil {
			return fmt.Errorf("reorder task %s: %w", id, err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("task %q not found or not in ready/approved status", id)
		}
	}
	return tx.Commit()
}

func cloneMetadata(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(meta))
	for k, v := range meta {
		cloned[k] = v
	}
	return cloned
}

// sqlPlaceholders returns a comma-separated string of N question marks for use
// in SQL IN clauses. The caller must ensure n > 0.
// Safety: this generates only literal "?" characters — no user input is interpolated.
func sqlPlaceholders(n int) string {
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func execWithBusyRetry(ctx context.Context, fn func() (sql.Result, error)) (sql.Result, error) {
	const maxAttempts = 5
	backoff := 40 * time.Millisecond
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		res, err := fn()
		if err == nil {
			return res, nil
		}
		if !isSQLiteBusyError(err) || attempt == maxAttempts-1 {
			return nil, err
		}
		lastErr = err

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if backoff < 500*time.Millisecond {
			backoff *= 2
		}
	}
	return nil, lastErr
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

// --- scan helpers ---

func scanTask(row *sql.Row) (Task, error) {
	var t Task
	var labelsJSON, metadataJSON string
	err := row.Scan(
		&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.Type, &t.Assignee, &labelsJSON, &t.EstimateMinutes,
		&t.ParentID, &t.Acceptance, &t.Project, &t.ErrorLog,
		&metadataJSON, &t.ActualDurationS, &t.IterationsUsed,
		&t.AttemptCount, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return t, fmt.Errorf("scan task: %w", err)
	}
	if labelsJSON != "" && labelsJSON != "[]" {
		if err := json.Unmarshal([]byte(labelsJSON), &t.Labels); err != nil {
			return t, fmt.Errorf("unmarshal labels for task %s: %w", t.ID, err)
		}
	}
	if metadataJSON != "" && metadataJSON != "{}" {
		if err := json.Unmarshal([]byte(metadataJSON), &t.Metadata); err != nil {
			return t, fmt.Errorf("unmarshal metadata for task %s: %w", t.ID, err)
		}
	}
	return t, nil
}

func scanTaskRows(rows *sql.Rows) (Task, error) {
	var t Task
	var labelsJSON, metadataJSON string
	err := rows.Scan(
		&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.Type, &t.Assignee, &labelsJSON, &t.EstimateMinutes,
		&t.ParentID, &t.Acceptance, &t.Project, &t.ErrorLog,
		&metadataJSON, &t.ActualDurationS, &t.IterationsUsed,
		&t.AttemptCount, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return t, fmt.Errorf("scan task: %w", err)
	}
	if labelsJSON != "" && labelsJSON != "[]" {
		if err := json.Unmarshal([]byte(labelsJSON), &t.Labels); err != nil {
			return t, fmt.Errorf("unmarshal labels for task %s: %w", t.ID, err)
		}
	}
	if metadataJSON != "" && metadataJSON != "{}" {
		if err := json.Unmarshal([]byte(metadataJSON), &t.Metadata); err != nil {
			return t, fmt.Errorf("unmarshal metadata for task %s: %w", t.ID, err)
		}
	}
	return t, nil
}

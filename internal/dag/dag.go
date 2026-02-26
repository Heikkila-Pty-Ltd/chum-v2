package dag

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	_ "modernc.org/sqlite"
)

const taskTableSchema = `CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'open',
	priority INTEGER NOT NULL DEFAULT 0,
	type TEXT NOT NULL DEFAULT 'task',
	assignee TEXT NOT NULL DEFAULT '',
	labels TEXT NOT NULL DEFAULT '[]',
	estimate_minutes INTEGER NOT NULL DEFAULT 0,
	parent_id TEXT NOT NULL DEFAULT '',
	acceptance TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	error_log TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);`

const edgeTableSchema = `CREATE TABLE IF NOT EXISTS task_edges (
	from_task TEXT NOT NULL,
	to_task TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT 'beads',
	PRIMARY KEY (from_task, to_task),
	FOREIGN KEY (from_task) REFERENCES tasks(id) ON DELETE CASCADE,
	FOREIGN KEY (to_task) REFERENCES tasks(id) ON DELETE CASCADE
);`

const taskTargetsSchema = `CREATE TABLE IF NOT EXISTS task_targets (
	task_id TEXT NOT NULL,
	file_path TEXT NOT NULL,
	symbol_name TEXT NOT NULL DEFAULT '',
	symbol_kind TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (task_id, file_path, symbol_name),
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);`

const taskColumns = `id, title, description, status, priority, type, assignee, labels,
	estimate_minutes, parent_id, acceptance, project, error_log, created_at, updated_at`

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
	d := &DAG{db: db}
	if err := d.EnsureSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// DB returns the underlying database connection.
func (d *DAG) DB() *sql.DB { return d.db }

// Close closes the underlying database connection.
func (d *DAG) Close() error { return d.db.Close() }

// EnsureSchema creates the tasks, task_edges, and task_targets tables
// if they don't exist, and runs any necessary migrations.
func (d *DAG) EnsureSchema(ctx context.Context) error {
	for _, ddl := range []string{taskTableSchema, edgeTableSchema, taskTargetsSchema} {
		if _, err := d.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("ensure schema: %w", err)
		}
	}
	// Migration: add source column to task_edges if missing (existing DBs).
	if err := d.migrateEdgeSource(ctx); err != nil {
		return err
	}
	return nil
}

// migrateEdgeSource adds the source column to task_edges if it doesn't exist.
func (d *DAG) migrateEdgeSource(ctx context.Context) error {
	rows, err := d.db.QueryContext(ctx, "PRAGMA table_info(task_edges)")
	if err != nil {
		return fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()
	hasSource := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan pragma: %w", err)
		}
		if name == "source" {
			hasSource = true
		}
	}
	if !hasSource {
		_, err := d.db.ExecContext(ctx,
			"ALTER TABLE task_edges ADD COLUMN source TEXT NOT NULL DEFAULT 'beads'")
		if err != nil {
			return fmt.Errorf("migrate edge source: %w", err)
		}
	}
	return nil
}

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
	labelsJSON, _ := json.Marshal(t.Labels)
	if t.Labels == nil {
		labelsJSON = []byte("[]")
	}
	status := t.Status
	if status == "" {
		status = "open"
	}
	taskType := t.Type
	if taskType == "" {
		taskType = "task"
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO tasks
		(id, title, description, status, priority, type, assignee, labels,
		 estimate_minutes, parent_id, acceptance, project, error_log)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Description, status, t.Priority, taskType,
		t.Assignee, string(labelsJSON), t.EstimateMinutes,
		t.ParentID, t.Acceptance, t.Project, t.ErrorLog,
	)
	if err != nil {
		return "", fmt.Errorf("insert task: %w", err)
	}
	return t.ID, nil
}

// GetTask retrieves a task by ID.
func (d *DAG) GetTask(ctx context.Context, id string) (Task, error) {
	row := d.db.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE id = ?", id)
	return scanTask(row)
}

// ListTasks returns tasks for a project, optionally filtering by statuses.
func (d *DAG) ListTasks(ctx context.Context, project string, statuses ...string) ([]Task, error) {
	query := "SELECT " + taskColumns + " FROM tasks WHERE project = ?"
	args := []any{project}
	if len(statuses) > 0 {
		placeholders := strings.Repeat("?,", len(statuses))
		placeholders = placeholders[:len(placeholders)-1]
		query += " AND status IN (" + placeholders + ")"
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
	}
	for k, v := range fields {
		if !allowed[k] {
			return fmt.Errorf("field %q is not updatable", k)
		}
		if k == "labels" {
			if labels, ok := v.([]string); ok {
				b, _ := json.Marshal(labels)
				v = string(b)
			}
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", k))
		args = append(args, v)
	}
	setClauses = append(setClauses, "updated_at = datetime('now')")
	args = append(args, id)
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	res, err := d.db.ExecContext(ctx, query, args...)
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

// GetReadyNodes returns tasks with status="ready" whose dependencies are all "completed".
func (d *DAG) GetReadyNodes(ctx context.Context, project string) ([]Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks t
		WHERE t.project = ? AND t.status = 'ready'
		AND NOT EXISTS (
			SELECT 1 FROM task_edges e
			LEFT JOIN tasks dep ON dep.id = e.to_task
			WHERE e.from_task = t.id
			AND (dep.id IS NULL OR dep.status != 'completed')
		)
		ORDER BY t.priority ASC, t.created_at ASC`
	rows, err := d.db.QueryContext(ctx, query, project)
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

// AddEdge creates a dependency: from depends on to. Source defaults to "beads".
func (d *DAG) AddEdge(ctx context.Context, from, to string) error {
	return d.AddEdgeWithSource(ctx, from, to, "beads")
}

// AddEdgeWithSource creates a dependency with an explicit source tag.
// Source is "beads" for hand-drawn edges or "ast" for auto-generated fences.
func (d *DAG) AddEdgeWithSource(ctx context.Context, from, to, source string) error {
	if from == to {
		return errors.New("cannot add self-edge")
	}
	_, err := d.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO task_edges (from_task, to_task, source) VALUES (?, ?, ?)",
		from, to, source)
	if err != nil {
		return fmt.Errorf("add edge: %w", err)
	}
	return nil
}

// DeleteEdgesBySource removes all edges with the given source for tasks in a project.
func (d *DAG) DeleteEdgesBySource(ctx context.Context, project, source string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM task_edges WHERE source = ? AND (
		from_task IN (SELECT id FROM tasks WHERE project = ?) OR
		to_task IN (SELECT id FROM tasks WHERE project = ?))`,
		source, project, project)
	if err != nil {
		return fmt.Errorf("delete edges by source: %w", err)
	}
	return nil
}

// RemoveEdge removes a dependency.
func (d *DAG) RemoveEdge(ctx context.Context, from, to string) error {
	_, err := d.db.ExecContext(ctx,
		"DELETE FROM task_edges WHERE from_task = ? AND to_task = ?",
		from, to)
	return err
}

// --- task_targets methods ---

// SetTaskTargets replaces all targets for a task.
func (d *DAG) SetTaskTargets(ctx context.Context, taskID string, targets []TaskTarget) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

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

// --- scan helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row *sql.Row) (Task, error) {
	var t Task
	var labelsJSON string
	err := row.Scan(
		&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.Type, &t.Assignee, &labelsJSON, &t.EstimateMinutes,
		&t.ParentID, &t.Acceptance, &t.Project, &t.ErrorLog,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return t, fmt.Errorf("scan task: %w", err)
	}
	_ = json.Unmarshal([]byte(labelsJSON), &t.Labels)
	return t, nil
}

func scanTaskRows(rows *sql.Rows) (Task, error) {
	var t Task
	var labelsJSON string
	err := rows.Scan(
		&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.Type, &t.Assignee, &labelsJSON, &t.EstimateMinutes,
		&t.ParentID, &t.Acceptance, &t.Project, &t.ErrorLog,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return t, fmt.Errorf("scan task: %w", err)
	}
	_ = json.Unmarshal([]byte(labelsJSON), &t.Labels)
	return t, nil
}

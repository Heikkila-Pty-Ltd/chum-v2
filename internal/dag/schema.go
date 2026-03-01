package dag

import (
	"context"
	"database/sql"
	"fmt"
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
		return fmt.Errorf("migrate schema: %w", err)
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

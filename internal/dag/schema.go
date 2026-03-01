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
	metadata TEXT NOT NULL DEFAULT '{}',
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

const decisionsTableSchema = `CREATE TABLE IF NOT EXISTS decisions (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	context TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);`

const decisionAlternativesTableSchema = `CREATE TABLE IF NOT EXISTS decision_alternatives (
	id TEXT PRIMARY KEY,
	decision_id TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	reasoning TEXT NOT NULL DEFAULT '',
	selected INTEGER NOT NULL DEFAULT 0,
	uct_score REAL NOT NULL DEFAULT 0,
	visits INTEGER NOT NULL DEFAULT 0,
	reward REAL NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	FOREIGN KEY (decision_id) REFERENCES decisions(id) ON DELETE CASCADE
);`

const taskColumns = `id, title, description, status, priority, type, assignee, labels,
	estimate_minutes, parent_id, acceptance, project, error_log, metadata, created_at, updated_at`

// EnsureSchema creates the tasks, task_edges, and task_targets tables
// if they don't exist, and runs any necessary migrations.
func (d *DAG) EnsureSchema(ctx context.Context) error {
	for _, ddl := range []string{taskTableSchema, edgeTableSchema, taskTargetsSchema, decisionsTableSchema, decisionAlternativesTableSchema} {
		if _, err := d.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("ensure schema: %w", err)
		}
	}
	if err := d.migrateEdgeSource(ctx); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if err := d.migrateTaskMetadata(ctx); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// migrateAddColumn adds a column to a table if it doesn't already exist.
// Uses PRAGMA table_info to check for the column's presence before ALTER TABLE.
func (d *DAG) migrateAddColumn(ctx context.Context, table, column, typedef string) error {
	rows, err := d.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan pragma: %w", err)
		}
		if name == column {
			found = true
		}
	}
	if !found {
		_, err := d.db.ExecContext(ctx,
			fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, typedef))
		if err != nil {
			return fmt.Errorf("add column %s.%s: %w", table, column, err)
		}
	}
	return nil
}

// migrateEdgeSource adds the source column to task_edges if it doesn't exist.
func (d *DAG) migrateEdgeSource(ctx context.Context) error {
	return d.migrateAddColumn(ctx, "task_edges", "source", "TEXT NOT NULL DEFAULT 'beads'")
}

// migrateTaskMetadata adds the metadata column to tasks if it doesn't exist.
func (d *DAG) migrateTaskMetadata(ctx context.Context) error {
	return d.migrateAddColumn(ctx, "tasks", "metadata", "TEXT NOT NULL DEFAULT '{}'")
}

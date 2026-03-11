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
	actual_duration_sec INTEGER NOT NULL DEFAULT 0,
	iterations_used INTEGER NOT NULL DEFAULT 0,
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

const planningSnapshotsTableSchema = `CREATE TABLE IF NOT EXISTS planning_snapshots (
	session_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	project TEXT NOT NULL DEFAULT '',
	phase TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	snapshot_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);`

const systemStateTableSchema = `CREATE TABLE IF NOT EXISTS system_state (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL DEFAULT '',
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);`

const indexDecisionsTaskID = `CREATE INDEX IF NOT EXISTS idx_decisions_task_id ON decisions(task_id);`
const indexAlternativesDecisionID = `CREATE INDEX IF NOT EXISTS idx_alternatives_decision_id ON decision_alternatives(decision_id);`
const indexPlanningSnapshotsTaskUpdated = `CREATE INDEX IF NOT EXISTS idx_planning_snapshots_task_updated ON planning_snapshots(task_id, updated_at DESC);`

const beadsSyncMapTableSchema = `CREATE TABLE IF NOT EXISTS beads_sync_map (
	project TEXT NOT NULL,
	issue_id TEXT NOT NULL,
	task_id TEXT NOT NULL,
	last_fingerprint TEXT NOT NULL DEFAULT '',
	admitted_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (project, issue_id),
	UNIQUE (task_id),
	FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);`

const beadsSyncOutboxTableSchema = `CREATE TABLE IF NOT EXISTS beads_sync_outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	issue_id TEXT NOT NULL,
	task_id TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '{}',
	idempotency_key TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT 'pending',
	attempts INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 5,
	next_attempt_at DATETIME NOT NULL DEFAULT (datetime('now')),
	last_error TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
	UNIQUE (project, idempotency_key)
);`

const beadsSyncCursorTableSchema = `CREATE TABLE IF NOT EXISTS beads_sync_cursor (
	project TEXT PRIMARY KEY,
	cursor_value TEXT NOT NULL DEFAULT '',
	last_scan_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);`

const beadsSyncAuditTableSchema = `CREATE TABLE IF NOT EXISTS beads_sync_audit (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	issue_id TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	event_kind TEXT NOT NULL,
	decision TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	fingerprint TEXT NOT NULL DEFAULT '',
	details TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);`

const indexBeadsSyncMapTask = `CREATE INDEX IF NOT EXISTS idx_beads_sync_map_task ON beads_sync_map(task_id);`
const indexBeadsSyncOutboxStateNext = `CREATE INDEX IF NOT EXISTS idx_beads_sync_outbox_state_next ON beads_sync_outbox(state, next_attempt_at, id);`
const indexBeadsSyncAuditProjectCreated = `CREATE INDEX IF NOT EXISTS idx_beads_sync_audit_project_created ON beads_sync_audit(project, created_at DESC);`
const indexBeadsSyncAuditIssue = `CREATE INDEX IF NOT EXISTS idx_beads_sync_audit_issue ON beads_sync_audit(project, issue_id, created_at DESC);`

const taskColumns = `id, title, description, status, priority, type, assignee, labels,
	estimate_minutes, parent_id, acceptance, project, error_log, metadata,
	actual_duration_sec, iterations_used, created_at, updated_at`

// EnsureSchema creates the tasks, task_edges, and task_targets tables
// if they don't exist, and runs any necessary migrations.
func (d *DAG) EnsureSchema(ctx context.Context) error {
	for _, ddl := range []string{
		taskTableSchema,
		edgeTableSchema,
		taskTargetsSchema,
		decisionsTableSchema,
		decisionAlternativesTableSchema,
		planningSnapshotsTableSchema,
		systemStateTableSchema,
		indexDecisionsTaskID,
		indexAlternativesDecisionID,
		indexPlanningSnapshotsTaskUpdated,
		beadsSyncMapTableSchema,
		beadsSyncOutboxTableSchema,
		beadsSyncCursorTableSchema,
		beadsSyncAuditTableSchema,
		indexBeadsSyncMapTask,
		indexBeadsSyncOutboxStateNext,
		indexBeadsSyncAuditProjectCreated,
		indexBeadsSyncAuditIssue,
	} {
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
	if err := d.migrateTaskExecMetrics(ctx); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// migrateAddColumn adds a column to a table if it doesn't already exist.
// Uses PRAGMA table_info to check for the column's presence before ALTER TABLE.
// Table and column names are validated via character allowlist to prevent SQL injection.
func (d *DAG) migrateAddColumn(ctx context.Context, table, column, typedef string) error {
	// Validate identifiers to prevent SQL injection (internal callers only, but defense in depth).
	for _, ident := range []string{table, column} {
		for _, ch := range ident {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
				return fmt.Errorf("invalid identifier %q", ident)
			}
		}
	}
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

// migrateTaskExecMetrics adds execution metric columns to tasks if they don't exist.
func (d *DAG) migrateTaskExecMetrics(ctx context.Context) error {
	if err := d.migrateAddColumn(ctx, "tasks", "actual_duration_sec", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return d.migrateAddColumn(ctx, "tasks", "iterations_used", "INTEGER NOT NULL DEFAULT 0")
}

package perf

import (
	"database/sql"
	"fmt"
	"strings"
)

const schema = `
CREATE TABLE IF NOT EXISTS perf_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider_key TEXT NOT NULL,
	agent TEXT NOT NULL,
	model TEXT NOT NULL DEFAULT '',
	tier TEXT NOT NULL,
	success INTEGER NOT NULL,
	duration_s REAL NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_perf_runs_tier ON perf_runs(tier);
CREATE INDEX IF NOT EXISTS idx_perf_runs_provider ON perf_runs(provider_key);
`

// v2 adds token and cost columns. ALTER TABLE ADD COLUMN is idempotent in
// practice: if the column already exists, SQLite returns "duplicate column"
// which we ignore. Any other error (lock, I/O) is surfaced.
var v2Columns = []string{
	"ALTER TABLE perf_runs ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE perf_runs ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE perf_runs ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0",
}

// v3 adds task_id column and performance indexes.
var v3Migrations = []string{
	"ALTER TABLE perf_runs ADD COLUMN task_id TEXT NOT NULL DEFAULT ''",
}

var v3Indexes = []string{
	"CREATE INDEX IF NOT EXISTS idx_perf_runs_created ON perf_runs(created_at)",
	"CREATE INDEX IF NOT EXISTS idx_perf_runs_task ON perf_runs(task_id)",
}

// Migrate creates the perf_runs table and applies all schema migrations.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// v2: add token/cost columns (ignore "duplicate column" errors only).
	for _, alter := range v2Columns {
		if _, err := db.Exec(alter); err != nil {
			if strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return fmt.Errorf("perf migration: %w", err)
		}
	}
	// v3: add task_id column (ignore "duplicate column" errors).
	for _, alter := range v3Migrations {
		if _, err := db.Exec(alter); err != nil {
			if strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return fmt.Errorf("perf migration: %w", err)
		}
	}
	// v3: add performance indexes.
	for _, idx := range v3Indexes {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("perf migration: %w", err)
		}
	}
	return nil
}

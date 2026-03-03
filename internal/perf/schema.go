package perf

import "database/sql"

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

// Migrate creates the perf_runs table if it doesn't exist.
func Migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}

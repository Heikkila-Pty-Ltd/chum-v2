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

// v2 adds token and cost columns. ALTER TABLE ADD COLUMN is idempotent in
// practice: if the column already exists, SQLite returns an error that we
// silently swallow.
var v2Columns = []string{
	"ALTER TABLE perf_runs ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE perf_runs ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE perf_runs ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0",
}

// Migrate creates the perf_runs table and applies all schema migrations.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// v2: add token/cost columns (ignore "duplicate column" errors).
	for _, alter := range v2Columns {
		_, _ = db.Exec(alter)
	}
	return nil
}

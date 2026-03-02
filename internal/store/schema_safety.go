package store

// schemaSafety contains the safety_blocks table for time-bounded guards.
const schemaSafety = `
CREATE TABLE IF NOT EXISTS safety_blocks (
	scope TEXT NOT NULL,
	block_type TEXT NOT NULL,
	blocked_until DATETIME NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	metadata TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY(scope, block_type)
);

CREATE INDEX IF NOT EXISTS idx_safety_blocks_blocked_until ON safety_blocks(blocked_until);
`

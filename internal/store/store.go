// Package store provides SQLite-backed persistence for CHUM v2 orchestration state.
//
// The store is split into focused sub-stores, each with its own interface and schema:
//   - TraceStore: execution traces and graph trace events
//   - SafetyStore: safety blocks and task validation guards
//   - LessonStore: lessons learned with FTS5 full-text search
//
// All sub-stores share a single SQLite database connection with WAL mode
// and single-writer enforcement for safe concurrent access.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store provides SQLite-backed persistence for CHUM v2 state.
// It implements TraceStore, SafetyStore, and LessonStore.
type Store struct {
	db *sql.DB
}

// schema concatenates all sub-store schemas.
var schema = schemaTraces + schemaSafety + schemaLessons

// Open creates or opens a SQLite database at the given path and applies all schemas.
// Uses WAL mode for concurrent reads and single-writer serialization.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sql.DB for advanced queries.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Compile-time interface checks.
var _ TraceStore = (*Store)(nil)
var _ SafetyStore = (*Store)(nil)
var _ LessonStore = (*Store)(nil)

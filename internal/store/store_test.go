package store

import (
	"path/filepath"
	"testing"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAndClose(t *testing.T) {
	s := tempStore(t)
	if s.DB() == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestOpenCreatesAllTables(t *testing.T) {
	s := tempStore(t)

	tables := []string{
		"execution_traces", "trace_events", "graph_trace_events",
		"safety_blocks", "lessons",
	}
	for _, table := range tables {
		var count int
		err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('` + table + `')`).Scan(&count)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if count == 0 {
			t.Errorf("table %s not created", table)
		}
	}
}

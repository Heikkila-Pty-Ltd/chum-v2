package beadsbridge

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"

	_ "modernc.org/sqlite"
)

type stubBeadsStore struct {
	issues map[string]beads.Issue
	ready  []beads.Issue
}

func (s *stubBeadsStore) List(_ context.Context, _ int) ([]beads.Issue, error) {
	var out []beads.Issue
	for _, v := range s.issues {
		out = append(out, v)
	}
	return out, nil
}

func (s *stubBeadsStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) {
	return append([]beads.Issue(nil), s.ready...), nil
}

func (s *stubBeadsStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	return s.issues[issueID], nil
}

func (s *stubBeadsStore) Close(_ context.Context, _, _ string) error { return nil }

func (s *stubBeadsStore) Create(_ context.Context, _ beads.CreateParams) (string, error) {
	return "", nil
}

func (s *stubBeadsStore) Update(_ context.Context, _ string, _ map[string]string) error { return nil }

func (s *stubBeadsStore) Children(_ context.Context, _ string) ([]beads.Issue, error) {
	return nil, nil
}

func (s *stubBeadsStore) AddDependency(_ context.Context, _, _ string) error { return nil }

func newBridgeTestDAG(t *testing.T) *dag.DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	d := dag.NewDAG(db)
	if err := d.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestScanner_DryRunDoesNotMutateDAG(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	client := &stubBeadsStore{
		issues: map[string]beads.Issue{
			"bd-1": {
				ID:     "bd-1",
				Title:  "Bridge task",
				Status: "ready",
				Labels: []string{"chum-canary"},
			},
		},
		ready: []beads.Issue{{ID: "bd-1"}},
	}

	s := &Scanner{
		DAG: d,
		Config: config.BeadsBridge{
			Enabled:     true,
			DryRun:      true,
			CanaryLabel: "chum-canary",
		},
		Logger: testLogger(),
	}
	res, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("scan dry-run: %v", err)
	}
	if res.Candidates != 1 || res.GatePassed != 1 {
		t.Fatalf("unexpected scan result: %+v", res)
	}
	tasks, err := d.ListTasks(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("dry-run should not create tasks, got %d", len(tasks))
	}
	audit, err := d.ListBeadsAudit(context.Background(), "proj", 100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audit) == 0 {
		t.Fatal("expected audit rows from dry-run")
	}
}

func TestScanner_AdmitsAndDedupesReplay(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	client := &stubBeadsStore{
		issues: map[string]beads.Issue{
			"bd-1": {
				ID:       "bd-1",
				Title:    "Bridge task",
				Status:   "ready",
				Labels:   []string{"chum-canary"},
				Priority: 1,
			},
		},
		ready: []beads.Issue{{ID: "bd-1"}},
	}
	s := &Scanner{
		DAG: d,
		Config: config.BeadsBridge{
			Enabled:     true,
			DryRun:      false,
			CanaryLabel: "chum-canary",
		},
		Logger: testLogger(),
	}

	first, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.Admitted != 1 {
		t.Fatalf("expected one admission, got %+v", first)
	}
	second, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second.Deduped != 1 {
		t.Fatalf("expected replay dedupe, got %+v", second)
	}

	tasks, err := d.ListTasks(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected exactly one task after replay, got %d", len(tasks))
	}
}

func TestScanner_ProjectsMappedDependencies(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	client := &stubBeadsStore{
		issues: map[string]beads.Issue{
			"bd-a": {
				ID:     "bd-a",
				Title:  "A",
				Status: "ready",
				Labels: []string{"chum-canary"},
			},
			"bd-b": {
				ID:     "bd-b",
				Title:  "B",
				Status: "ready",
				Labels: []string{"chum-canary"},
				Dependencies: []beads.Dependency{
					{IssueID: "bd-b", DependsOnID: "bd-a", Type: "depends_on"},
				},
			},
		},
		ready: []beads.Issue{{ID: "bd-a"}, {ID: "bd-b"}},
	}
	s := &Scanner{
		DAG: d,
		Config: config.BeadsBridge{
			Enabled:     true,
			DryRun:      false,
			CanaryLabel: "chum-canary",
		},
		Logger: testLogger(),
	}

	res, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Admitted != 2 {
		t.Fatalf("expected 2 admissions, got %+v", res)
	}
	if res.EdgesProjected != 1 {
		t.Fatalf("expected 1 projected edge, got %+v", res)
	}
	deps, err := d.GetDependencies(context.Background(), "bd-b")
	if err != nil {
		t.Fatalf("get dependencies: %v", err)
	}
	if len(deps) != 1 || deps[0] != "bd-a" {
		t.Fatalf("unexpected dependencies for bd-b: %v", deps)
	}
}

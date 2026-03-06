package beadsbridge

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"

	_ "modernc.org/sqlite"
)

type stubBeadsStore struct {
	issues       map[string]beads.Issue
	ready        []beads.Issue
	dynamicReady bool
	updates      map[string]map[string]string
}

func (s *stubBeadsStore) List(_ context.Context, _ int) ([]beads.Issue, error) {
	var out []beads.Issue
	for _, v := range s.issues {
		out = append(out, v)
	}
	return out, nil
}

func (s *stubBeadsStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) {
	if s.dynamicReady {
		var out []beads.Issue
		for _, issue := range s.issues {
			switch strings.ToLower(strings.TrimSpace(issue.Status)) {
			case "open", "ready":
				out = append(out, issue)
			}
		}
		return out, nil
	}
	return append([]beads.Issue(nil), s.ready...), nil
}

func (s *stubBeadsStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	return s.issues[issueID], nil
}

func (s *stubBeadsStore) Close(_ context.Context, _, _ string) error { return nil }

func (s *stubBeadsStore) Create(_ context.Context, _ beads.CreateParams) (string, error) {
	return "", nil
}

func (s *stubBeadsStore) Update(_ context.Context, issueID string, fields map[string]string) error {
	if s.updates == nil {
		s.updates = make(map[string]map[string]string)
	}
	copied := make(map[string]string, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	s.updates[issueID] = copied

	issue, ok := s.issues[issueID]
	if !ok {
		return nil
	}
	if status, ok := fields["status"]; ok {
		issue.Status = status
	}
	s.issues[issueID] = issue
	return nil
}

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

func TestScanner_PrunesStaleProjectedDependencies(t *testing.T) {
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

	first, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.EdgesProjected != 1 {
		t.Fatalf("expected one projected edge on first scan, got %+v", first)
	}

	// Dependency removed in beads -> bridge should prune stale DAG edge.
	issueB := client.issues["bd-b"]
	issueB.Dependencies = nil
	client.issues["bd-b"] = issueB
	client.ready = []beads.Issue{{ID: "bd-b"}}

	second, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second.EdgesPruned != 1 {
		t.Fatalf("expected one pruned edge on second scan, got %+v", second)
	}
	deps, err := d.GetDependencies(context.Background(), "bd-b")
	if err != nil {
		t.Fatalf("get dependencies: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected stale dependency to be removed, got %v", deps)
	}
}

func TestScanner_ReplacesNonBridgeDependencySource(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	ctx := context.Background()

	if _, err := d.CreateTask(ctx, dag.Task{ID: "bd-a", Project: "proj", Status: string(types.StatusReady)}); err != nil {
		t.Fatalf("seed task bd-a: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{ID: "bd-b", Project: "proj", Status: string(types.StatusReady)}); err != nil {
		t.Fatalf("seed task bd-b: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "proj", "bd-a", "bd-a", "fp-a"); err != nil {
		t.Fatalf("seed mapping bd-a: %v", err)
	}
	if err := d.UpsertBeadsMapping(ctx, "proj", "bd-b", "bd-b", "fp-b"); err != nil {
		t.Fatalf("seed mapping bd-b: %v", err)
	}
	if err := d.AddEdgeWithSource(ctx, "bd-b", "bd-a", "ast"); err != nil {
		t.Fatalf("seed ast edge: %v", err)
	}

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
		ready: []beads.Issue{{ID: "bd-b"}},
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

	res, err := s.ScanProject(ctx, "proj", client)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.EdgesPruned != 1 {
		t.Fatalf("expected one pruned edge (ast replacement), got %+v", res)
	}
	if res.EdgesProjected != 1 {
		t.Fatalf("expected one projected edge after replacement, got %+v", res)
	}
	source, err := d.GetEdgeSource(ctx, "bd-b", "bd-a")
	if err != nil {
		t.Fatalf("get edge source: %v", err)
	}
	if source != "beads_bridge" {
		t.Fatalf("edge source = %q, want beads_bridge", source)
	}
}

func TestScanner_ReadyReplayPromotesMappedTaskToReady(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	client := &stubBeadsStore{
		issues: map[string]beads.Issue{
			"bd-1": {
				ID:     "bd-1",
				Title:  "Bridge task",
				Status: "open",
				Labels: []string{"chum-canary"},
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
	fp := FingerprintIssue(client.issues["bd-1"])
	if _, err := d.CreateTask(context.Background(), dag.Task{
		ID:      "bd-1",
		Project: "proj",
		Title:   "Bridge task",
		Status:  string(types.StatusOpen),
	}); err != nil {
		t.Fatalf("seed mapped task: %v", err)
	}
	if err := d.UpsertBeadsMapping(context.Background(), "proj", "bd-1", "bd-1", fp); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	res, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Deduped != 1 {
		t.Fatalf("expected dedupe path, got %+v", res)
	}
	task, err := d.GetTask(context.Background(), "bd-1")
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.Status != string(types.StatusReady) {
		t.Fatalf("expected mapped task promoted to ready, got %s", task.Status)
	}
}

func TestScanner_ClosedDependencySyncUnblocksReadyWork(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	client := &stubBeadsStore{
		issues: map[string]beads.Issue{
			"bd-dep": {
				ID:     "bd-dep",
				Title:  "Dependency",
				Status: "closed",
				Labels: []string{"chum-canary"},
			},
			"bd-task": {
				ID:     "bd-task",
				Title:  "Ready candidate",
				Status: "open",
				Labels: []string{"chum-canary"},
				Dependencies: []beads.Dependency{
					{IssueID: "bd-task", DependsOnID: "bd-dep", Type: "blocks"},
				},
			},
		},
		ready: []beads.Issue{{ID: "bd-task"}},
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

	if _, err := d.CreateTask(context.Background(), dag.Task{
		ID:      "bd-dep",
		Project: "proj",
		Title:   "Dependency",
		Status:  string(types.StatusNeedsReview),
	}); err != nil {
		t.Fatalf("seed dependency task: %v", err)
	}
	if err := d.UpsertBeadsMapping(context.Background(), "proj", "bd-dep", "bd-dep", FingerprintIssue(client.issues["bd-dep"])); err != nil {
		t.Fatalf("seed dependency mapping: %v", err)
	}

	res, err := s.ScanProject(context.Background(), "proj", client)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Admitted != 1 {
		t.Fatalf("expected admission for ready task, got %+v", res)
	}

	depTask, err := d.GetTask(context.Background(), "bd-dep")
	if err != nil {
		t.Fatalf("load dependency task: %v", err)
	}
	if depTask.Status != string(types.StatusCompleted) {
		t.Fatalf("expected closed dependency promoted to completed, got %s", depTask.Status)
	}
}

func TestScanner_ReopensBlockedIssueWhenDependenciesAreClosed(t *testing.T) {
	t.Parallel()
	d := newBridgeTestDAG(t)
	client := &stubBeadsStore{
		issues: map[string]beads.Issue{
			"bd-dep": {
				ID:     "bd-dep",
				Title:  "Closed dependency",
				Status: "closed",
				Labels: []string{"chum-canary"},
			},
			"bd-blocked": {
				ID:     "bd-blocked",
				Title:  "Blocked task",
				Status: "blocked",
				Labels: []string{"chum-canary"},
				Dependencies: []beads.Dependency{
					{IssueID: "bd-blocked", DependsOnID: "bd-dep", Type: "blocks"},
				},
			},
		},
		dynamicReady: true,
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
	if got := client.updates["bd-blocked"]["status"]; got != "open" {
		t.Fatalf("blocked issue should be reopened to open, got %q", got)
	}
	if res.Admitted != 1 {
		t.Fatalf("expected reopened issue to be admitted on same scan, got %+v", res)
	}
}

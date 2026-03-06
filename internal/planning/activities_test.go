package planning

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
	"go.temporal.io/sdk/testsuite"

	_ "modernc.org/sqlite"
)

type planningStubBeadsStore struct {
	createIDs   []string
	createCalls int
	closeCalls  int
}

func (s *planningStubBeadsStore) List(_ context.Context, _ int) ([]beads.Issue, error) {
	return nil, nil
}
func (s *planningStubBeadsStore) Ready(_ context.Context, _ int) ([]beads.Issue, error) {
	return nil, nil
}
func (s *planningStubBeadsStore) Show(_ context.Context, issueID string) (beads.Issue, error) {
	return beads.Issue{ID: issueID, Status: "open"}, nil
}
func (s *planningStubBeadsStore) Close(_ context.Context, _, _ string) error {
	s.closeCalls++
	return nil
}
func (s *planningStubBeadsStore) Create(_ context.Context, _ beads.CreateParams) (string, error) {
	s.createCalls++
	if len(s.createIDs) > 0 {
		id := s.createIDs[0]
		s.createIDs = s.createIDs[1:]
		return id, nil
	}
	return "bd-sub-1", nil
}
func (s *planningStubBeadsStore) Update(_ context.Context, _ string, _ map[string]string) error {
	return nil
}
func (s *planningStubBeadsStore) Children(_ context.Context, _ string) ([]beads.Issue, error) {
	return nil, nil
}
func (s *planningStubBeadsStore) AddDependency(_ context.Context, _, _ string) error {
	return nil
}

func newPlanningTestDAG(t *testing.T) *dag.DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
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

func TestCreatePlanSubtasksActivity_RejectsEmptyBeadsIssueID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	d := newPlanningTestDAG(t)
	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "goal-1",
		Title:   "Goal",
		Project: "proj",
		Status:  string(types.StatusReady),
	}); err != nil {
		t.Fatalf("create goal task: %v", err)
	}

	store := &planningStubBeadsStore{
		createIDs: []string{""},
	}
	pa := &PlanningActivities{
		DAG: d,
		Config: &config.Config{
			BeadsBridge: config.BeadsBridge{
				Enabled:       true,
				IngressPolicy: "beads_only",
			},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		BeadsClients: map[string]beads.Store{
			"proj": store,
		},
	}

	req := PlanningRequest{
		GoalID:  "goal-1",
		Project: "proj",
	}
	steps := []types.DecompStep{
		{Title: "Step one", Description: "Do first thing", Acceptance: "first ok", Estimate: 8},
	}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(pa.CreatePlanSubtasksActivity)
	_, err := env.ExecuteActivity(pa.CreatePlanSubtasksActivity, req, steps)
	if err == nil {
		t.Fatal("expected empty beads issue id to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "empty issue id") {
		t.Fatalf("unexpected error: %v", err)
	}

	tasks, listErr := d.ListTasks(ctx, "proj")
	if listErr != nil {
		t.Fatalf("list tasks: %v", listErr)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected only goal task to remain, got %d tasks", len(tasks))
	}
	if store.closeCalls != 0 {
		t.Fatalf("expected no rollback closes for first empty id, got %d", store.closeCalls)
	}
}

func TestCreatePlanSubtasksActivity_LegacyIngressFallsBackToDAGOnlySubtasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	d := newPlanningTestDAG(t)
	if _, err := d.CreateTask(ctx, dag.Task{
		ID:      "goal-1",
		Title:   "Goal",
		Project: "proj",
		Status:  string(types.StatusReady),
	}); err != nil {
		t.Fatalf("create goal task: %v", err)
	}

	pa := &PlanningActivities{
		DAG: d,
		Config: &config.Config{
			BeadsBridge: config.BeadsBridge{
				Enabled:       true,
				IngressPolicy: "legacy",
			},
		},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		BeadsClients: map[string]beads.Store{},
	}

	req := PlanningRequest{
		GoalID:  "goal-1",
		Project: "proj",
	}
	steps := []types.DecompStep{
		{Title: "Step one", Description: "Do first thing", Acceptance: "first ok", Estimate: 8},
		{Title: "Step two", Description: "Do second thing", Acceptance: "second ok", Estimate: 9},
	}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(pa.CreatePlanSubtasksActivity)
	var ids []string
	value, err := env.ExecuteActivity(pa.CreatePlanSubtasksActivity, req, steps)
	if err != nil {
		t.Fatalf("legacy planning subtask creation failed: %v", err)
	}
	if err := value.Get(&ids); err != nil {
		t.Fatalf("decode activity result: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 subtasks, got %d", len(ids))
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			t.Fatalf("expected generated non-empty ID, got %q", id)
		}
		if _, err := d.GetBeadsMappingByTask(ctx, "proj", id); err == nil || !dag.IsNoRows(err) {
			t.Fatalf("expected no beads mapping for legacy planning subtask %s, got err=%v", id, err)
		}
	}
}

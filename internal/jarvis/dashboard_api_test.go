package jarvis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/engine"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
	"github.com/stretchr/testify/mock"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"
)

type stubWorkflowRun struct {
	id    string
	runID string
}

func (s stubWorkflowRun) GetID() string                          { return s.id }
func (s stubWorkflowRun) GetRunID() string                       { return s.runID }
func (s stubWorkflowRun) Get(context.Context, interface{}) error { return nil }
func (s stubWorkflowRun) GetWithOptions(context.Context, interface{}, client.WorkflowRunGetOptions) error {
	return nil
}

func testDashboardAPI(t *testing.T) (*API, *dag.DAG) {
	t.Helper()
	d := testDAG(t)
	e := NewEngine(d, nil, "test-queue", map[string]string{"chum": "/tmp/chum", "other": "/tmp/other"}, testLogger())
	api := &API{Engine: e, DAG: d, Logger: testLogger()}
	return api, d
}

func TestDashboardProjects(t *testing.T) {
	api, _ := testDashboardAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/projects", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		Projects []string `json:"projects"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(result.Projects))
	}
	// Should be sorted
	if result.Projects[0] != "chum" || result.Projects[1] != "other" {
		t.Errorf("projects = %v, want [chum other]", result.Projects)
	}
}

func TestDashboardGraph(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	// Create two tasks with an edge
	id1, _ := d.CreateTask(ctx, dag.Task{Title: "Task A", Project: "chum", Status: "completed"})
	id2, _ := d.CreateTask(ctx, dag.Task{Title: "Task B", Project: "chum", Status: "ready"})
	_ = d.AddEdge(ctx, id2, id1) // B depends on A

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/graph/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		Nodes []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"nodes"`
		Edges []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"edges"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(result.Edges))
	}
}

func TestDashboardGraphEmpty(t *testing.T) {
	api, _ := testDashboardAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/graph/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var result struct {
		Nodes []any `json:"nodes"`
		Edges []any `json:"edges"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Nodes == nil {
		t.Error("nodes should be empty array, not null")
	}
	if result.Edges == nil {
		t.Error("edges should be empty array, not null")
	}
}

func TestDashboardTasks(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	if _, err := d.CreateTask(ctx, dag.Task{Title: "Running task", Project: "chum", Status: "running"}); err != nil {
		t.Fatalf("CreateTask running: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Done task", Project: "chum", Status: "completed"}); err != nil {
		t.Fatalf("CreateTask done: %v", err)
	}

	// Unfiltered
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/tasks/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	var result struct {
		Tasks []dag.Task `json:"tasks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode unfiltered: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}

	// Filtered by status
	req = httptest.NewRequest(http.MethodGet, "/api/dashboard/tasks/chum?status=running", nil)
	w = httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	result.Tasks = nil
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("expected 1 running task, got %d", len(result.Tasks))
	}
	if result.Tasks[0].Status != "running" {
		t.Errorf("status = %q, want running", result.Tasks[0].Status)
	}
}

func TestDashboardTaskDetail(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, dag.Task{Title: "Detail test", Project: "chum", Status: "ready"})
	if err := d.UpsertPlanningSnapshot(ctx, dag.PlanningSnapshot{
		SessionID: "plan-1",
		GoalID:    id,
		Project:   "chum",
		Phase:     "approve_decomposition",
		Status:    "awaiting_approval",
		PlanSpec: &types.PlanSpec{
			ProblemStatement:   "Planning is opaque",
			DesiredOutcome:     "Show the plan",
			ExpectedPROutcome:  "Add dashboard planning visibility",
			Summary:            "Persist and expose a plan spec.",
			ChosenApproach:     types.PlanningApproach{Title: "Persist snapshot"},
			NonGoals:           []string{"Rewrite the planner"},
			Risks:              []string{"API drift"},
			ValidationStrategy: []string{"Dashboard API test"},
			Steps:              []types.DecompStep{{Title: "Add API", Description: "Expose planning", Acceptance: "Endpoint returns planning", Estimate: 10}},
		},
	}); err != nil {
		t.Fatalf("UpsertPlanningSnapshot: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/task/"+id, nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		Task         dag.Task `json:"task"`
		Dependencies []string `json:"dependencies"`
		Dependents   []string `json:"dependents"`
		Targets      []any    `json:"targets"`
		Decisions    []any    `json:"decisions"`
		Planning     *struct {
			SessionID string `json:"session_id"`
		} `json:"planning"`
		PlanningSessions []any `json:"planning_sessions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Task.ID != id {
		t.Errorf("task id = %q, want %q", result.Task.ID, id)
	}
	if result.Dependencies == nil {
		t.Error("dependencies should be empty array, not null")
	}
	if result.Planning == nil || result.Planning.SessionID != "plan-1" {
		t.Fatalf("expected planning snapshot in task detail, got %+v", result.Planning)
	}
	if len(result.PlanningSessions) != 1 {
		t.Fatalf("expected 1 planning session, got %d", len(result.PlanningSessions))
	}
}

func TestDashboardPlanningEndpoint(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, dag.Task{Title: "Planning endpoint", Project: "chum", Status: "ready"})
	if err := d.UpsertPlanningSnapshot(ctx, dag.PlanningSnapshot{
		SessionID: "plan-2",
		GoalID:    id,
		Project:   "chum",
		Phase:     "completed",
		Status:    "completed",
		History:   []types.PlanningPhaseEntry{{Phase: "completed", Status: "completed", Note: "done"}},
	}); err != nil {
		t.Fatalf("UpsertPlanningSnapshot: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/planning/"+id, nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		TaskID   string `json:"task_id"`
		Planning *struct {
			SessionID string `json:"session_id"`
		} `json:"planning"`
		Sessions []any `json:"sessions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.TaskID != id {
		t.Fatalf("task_id = %q, want %q", result.TaskID, id)
	}
	if result.Planning == nil || result.Planning.SessionID != "plan-2" {
		t.Fatalf("expected latest planning snapshot, got %+v", result.Planning)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(result.Sessions))
	}
}

func TestDashboardPlanningStart(t *testing.T) {
	d := testDAG(t)
	ctx := context.Background()
	if _, err := d.CreateTask(ctx, dag.Task{ID: "goal-1", Title: "Goal", Project: "chum", Status: "ready"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	tc := temporalmocks.NewClient(t)
	tc.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(stubWorkflowRun{id: "planning-chum-1", runID: "run-1"}, nil).Once()

	e := NewEngine(d, tc, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{
		Engine:               e,
		DAG:                  d,
		Logger:               testLogger(),
		PlanningDefaultAgent: "codex",
		PlanningCfg:          planning.PlanningCeremonyConfig{MaxCycles: 3},
	}

	body := strings.NewReader(`{"project":"chum","goal_id":"goal-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/planning/start", body)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		SessionID string `json:"session_id"`
		Started   bool   `json:"started"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result.Started {
		t.Fatal("expected started=true")
	}
	if !strings.HasPrefix(result.SessionID, "planning-chum-") {
		t.Fatalf("unexpected session id %q", result.SessionID)
	}
}

func TestDashboardPlanningSignal(t *testing.T) {
	d := testDAG(t)
	tc := temporalmocks.NewClient(t)
	tc.On("SignalWorkflow", mock.Anything, "planning-chum-1", "", planning.SignalNameSelect, "2").Return(nil).Once()

	e := NewEngine(d, tc, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, DAG: d, Logger: testLogger()}

	body := strings.NewReader(`{"action":"select","value":"2"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/planning/planning-chum-1/signal", body)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

func TestDashboardTaskNotFound(t *testing.T) {
	api, _ := testDashboardAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/task/nonexistent", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDashboardStats(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	if _, err := d.CreateTask(ctx, dag.Task{Title: "A", Project: "chum", Status: "completed", EstimateMinutes: 10}); err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "B", Project: "chum", Status: "completed", EstimateMinutes: 5}); err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "C", Project: "chum", Status: "running"}); err != nil {
		t.Fatalf("CreateTask C: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("total = %d, want 3", result.Total)
	}
	if result.ByStatus["completed"] != 2 {
		t.Errorf("completed = %d, want 2", result.ByStatus["completed"])
	}
	if result.ByStatus["running"] != 1 {
		t.Errorf("running = %d, want 1", result.ByStatus["running"])
	}
}

func TestDashboardTimeline(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	if _, err := d.CreateTask(ctx, dag.Task{Title: "First", Project: "chum", Status: "completed"}); err != nil {
		t.Fatalf("CreateTask first: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Second", Project: "chum", Status: "running"}); err != nil {
		t.Fatalf("CreateTask second: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/timeline/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var result struct {
		Tasks []dag.Task `json:"tasks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(result.Tasks))
	}
}

func TestDashboardOverviewGrouped(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	// Create a goal with children
	goalID, _ := d.CreateTask(ctx, dag.Task{Title: "Goal A", Project: "chum", Status: "open", Type: "goal"})
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Sub 1", Project: "chum", Status: "completed", ParentID: goalID}); err != nil {
		t.Fatalf("CreateTask sub1: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Sub 2", Project: "chum", Status: "failed", ParentID: goalID}); err != nil {
		t.Fatalf("CreateTask sub2: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Sub 3", Project: "chum", Status: "running", ParentID: goalID}); err != nil {
		t.Fatalf("CreateTask sub3: %v", err)
	}
	// Orphan task
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Orphan", Project: "chum", Status: "failed"}); err != nil {
		t.Fatalf("CreateTask orphan: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview-grouped/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		Goals []struct {
			Task struct {
				ID string `json:"id"`
			} `json:"task"`
			SubtaskTotal     int    `json:"subtask_total"`
			SubtaskCompleted int    `json:"subtask_completed"`
			SubtaskFailed    int    `json:"subtask_failed"`
			SubtaskRunning   int    `json:"subtask_running"`
			Health           string `json:"health"`
			Children         []struct {
				ID string `json:"id"`
			} `json:"children"`
		} `json:"goals"`
		Orphans []struct {
			ID string `json:"id"`
		} `json:"orphans"`
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
		Velocity struct {
			Completed24h int `json:"completed_24h"`
			Completed7d  int `json:"completed_7d"`
		} `json:"velocity"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(result.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(result.Goals))
	}

	g := result.Goals[0]
	if g.Task.ID != goalID {
		t.Errorf("goal id = %q, want %q", g.Task.ID, goalID)
	}
	if g.SubtaskTotal != 3 {
		t.Errorf("subtask_total = %d, want 3", g.SubtaskTotal)
	}
	if g.SubtaskCompleted != 1 {
		t.Errorf("subtask_completed = %d, want 1", g.SubtaskCompleted)
	}
	if g.SubtaskFailed != 1 {
		t.Errorf("subtask_failed = %d, want 1", g.SubtaskFailed)
	}
	if g.SubtaskRunning != 1 {
		t.Errorf("subtask_running = %d, want 1", g.SubtaskRunning)
	}
	// 1/3 = 33% > 30% threshold, so health is "failing"
	if g.Health != "failing" {
		t.Errorf("health = %q, want failing", g.Health)
	}
	if len(g.Children) != 3 {
		t.Errorf("children = %d, want 3", len(g.Children))
	}

	// Orphan should show up
	if len(result.Orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(result.Orphans))
	}

	if result.Total != 5 {
		t.Errorf("total = %d, want 5", result.Total)
	}
}

func TestDashboardOverviewGroupedEmpty(t *testing.T) {
	api, _ := testDashboardAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview-grouped/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var result struct {
		Goals   []any `json:"goals"`
		Orphans []any `json:"orphans"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Goals == nil {
		t.Error("goals should be empty array, not null")
	}
	if result.Orphans == nil {
		t.Error("orphans should be empty array, not null")
	}
}

func TestDashboardTaskPause(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, dag.Task{Title: "Running task", Project: "chum", Status: "running"})

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/task/"+id+"/pause", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	task, _ := d.GetTask(ctx, id)
	if task.Status != "ready" {
		t.Errorf("status = %q, want ready", task.Status)
	}
}

func TestDashboardTaskPauseNotRunning(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, dag.Task{Title: "Done task", Project: "chum", Status: "completed"})

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/task/"+id+"/pause", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestSystemPause(t *testing.T) {
	d := testDAG(t)
	tc := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)
	handle := temporalmocks.NewScheduleHandle(t)

	tc.On("ScheduleClient").Return(schedClient)
	schedClient.On("GetHandle", mock.Anything, engine.DispatcherScheduleID).Return(handle)
	handle.On("Pause", mock.Anything, client.SchedulePauseOptions{
		Note: "paused via API",
	}).Return(nil).Once()

	e := NewEngine(d, tc, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, DAG: d, Logger: testLogger()}

	req := httptest.NewRequest(http.MethodPost, "/api/system/pause", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	paused, err := d.IsGlobalPaused(context.Background())
	if err != nil {
		t.Fatalf("IsGlobalPaused: %v", err)
	}
	if !paused {
		t.Fatal("expected global pause enabled")
	}
}

func TestSystemResume(t *testing.T) {
	d := testDAG(t)
	if err := d.SetGlobalPaused(context.Background(), true); err != nil {
		t.Fatalf("SetGlobalPaused(true): %v", err)
	}

	tc := temporalmocks.NewClient(t)
	schedClient := temporalmocks.NewScheduleClient(t)
	handle := temporalmocks.NewScheduleHandle(t)

	tc.On("ScheduleClient").Return(schedClient).Times(2)
	schedClient.On("GetHandle", mock.Anything, engine.DispatcherScheduleID).Return(handle).Times(2)
	handle.On("Unpause", mock.Anything, client.ScheduleUnpauseOptions{
		Note: "resumed via API",
	}).Return(nil).Once()
	handle.On("Trigger", mock.Anything, client.ScheduleTriggerOptions{
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	}).Return(nil).Once()

	e := NewEngine(d, tc, "test-queue", map[string]string{"chum": "/tmp/chum"}, testLogger())
	api := &API{Engine: e, DAG: d, Logger: testLogger()}

	req := httptest.NewRequest(http.MethodPost, "/api/system/resume", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	paused, err := d.IsGlobalPaused(context.Background())
	if err != nil {
		t.Fatalf("IsGlobalPaused: %v", err)
	}
	if paused {
		t.Fatal("expected global pause disabled")
	}
}

func TestDashboardTaskKill(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, dag.Task{Title: "Running task", Project: "chum", Status: "running"})

	body := strings.NewReader(`{"reason":"test kill"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/task/"+id+"/kill", body)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	task, _ := d.GetTask(ctx, id)
	if task.Status != "failed" {
		t.Errorf("status = %q, want failed", task.Status)
	}
	if task.ErrorLog != "test kill" {
		t.Errorf("error_log = %q, want 'test kill'", task.ErrorLog)
	}
}

func TestDashboardTaskDecompose(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	id, _ := d.CreateTask(ctx, dag.Task{Title: "Big task", Project: "chum", Status: "ready"})

	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/task/"+id+"/decompose", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	task, _ := d.GetTask(ctx, id)
	if task.Status != "needs_refinement" {
		t.Errorf("status = %q, want needs_refinement", task.Status)
	}
}

func TestDashboardOverviewGroupedDisplayStatus(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	// Goal with mixed children — should show "has_failures" not "rejected"
	goalID, _ := d.CreateTask(ctx, dag.Task{Title: "Goal", Project: "chum", Status: "open", Type: "goal"})
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Child 1", Project: "chum", Status: "completed", ParentID: goalID}); err != nil {
		t.Fatalf("CreateTask child1: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Child 2", Project: "chum", Status: "failed", ParentID: goalID}); err != nil {
		t.Fatalf("CreateTask child2: %v", err)
	}
	if _, err := d.CreateTask(ctx, dag.Task{Title: "Child 3", Project: "chum", Status: "running", ParentID: goalID}); err != nil {
		t.Fatalf("CreateTask child3: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview-grouped/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var result struct {
		Goals []struct {
			DisplayStatus    string `json:"display_status"`
			TotalEstimateMin int    `json:"total_estimate_minutes"`
			TotalActualSec   int    `json:"total_actual_duration_sec"`
		} `json:"goals"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(result.Goals))
	}
	// has running + failed children, so display status should reflect that
	if result.Goals[0].DisplayStatus != "running" {
		t.Errorf("display_status = %q, want running", result.Goals[0].DisplayStatus)
	}
}

func TestDashboardOverviewGroupedEstimates(t *testing.T) {
	api, d := testDashboardAPI(t)
	ctx := context.Background()

	goalID, _ := d.CreateTask(ctx, dag.Task{Title: "Goal", Project: "chum", Status: "open", Type: "goal"})
	c1ID, _ := d.CreateTask(ctx, dag.Task{Title: "C1", Project: "chum", Status: "completed", ParentID: goalID, EstimateMinutes: 10})
	if err := d.UpdateTask(ctx, c1ID, map[string]any{"actual_duration_sec": 300}); err != nil {
		t.Fatalf("UpdateTask c1: %v", err)
	}
	c2ID, _ := d.CreateTask(ctx, dag.Task{Title: "C2", Project: "chum", Status: "running", ParentID: goalID, EstimateMinutes: 5})
	if err := d.UpdateTask(ctx, c2ID, map[string]any{"actual_duration_sec": 120}); err != nil {
		t.Fatalf("UpdateTask c2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/overview-grouped/chum", nil)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, req)

	var result struct {
		Goals []struct {
			TotalEstimateMin int `json:"total_estimate_minutes"`
			TotalActualSec   int `json:"total_actual_duration_sec"`
		} `json:"goals"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(result.Goals))
	}
	if result.Goals[0].TotalEstimateMin != 15 {
		t.Errorf("total_estimate_minutes = %d, want 15", result.Goals[0].TotalEstimateMin)
	}
	if result.Goals[0].TotalActualSec != 420 {
		t.Errorf("total_actual_duration_sec = %d, want 420", result.Goals[0].TotalActualSec)
	}
}

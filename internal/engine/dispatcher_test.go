package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/perf"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

type mockTaskStore struct {
	tasks          map[string][]dag.Task
	statusUpdates  map[string]string // taskID -> new status
	globalPaused   bool
	globalPauseSet bool // true = DB row exists (SetGlobalPaused was called)
	globalPauseErr error
}

func (m *mockTaskStore) GetReadyNodes(ctx context.Context, project string) ([]dag.Task, error) {
	return m.tasks[project], nil
}

func (m *mockTaskStore) GetTask(ctx context.Context, id string) (dag.Task, error) {
	return dag.Task{}, nil
}

func (m *mockTaskStore) CreateTask(ctx context.Context, t dag.Task) (string, error) {
	return "", nil
}

func (m *mockTaskStore) UpdateTask(ctx context.Context, id string, fields map[string]any) error {
	return nil
}

func (m *mockTaskStore) UpdateTaskStatus(ctx context.Context, id, status string) error {
	if m.statusUpdates != nil {
		m.statusUpdates[id] = status
	}
	return nil
}

func (m *mockTaskStore) CloseTask(ctx context.Context, id, status string) error {
	return nil
}

func (m *mockTaskStore) ListTasks(ctx context.Context, project string, statuses ...string) ([]dag.Task, error) {
	var result []dag.Task
	for _, t := range m.tasks[project] {
		for _, s := range statuses {
			if t.Status == s {
				result = append(result, t)
				break
			}
		}
	}
	return result, nil
}

func (m *mockTaskStore) CreateSubtasksAtomic(ctx context.Context, parentID string, tasks []dag.Task) ([]string, error) {
	return nil, nil
}

func (m *mockTaskStore) AddEdge(ctx context.Context, from, to string) error {
	return nil
}

func (m *mockTaskStore) AddEdgeWithSource(ctx context.Context, from, to, source string) error {
	return nil
}

func (m *mockTaskStore) RemoveEdge(ctx context.Context, from, to string) error {
	return nil
}

func (m *mockTaskStore) DeleteEdgesBySource(ctx context.Context, project, source string) error {
	return nil
}

func (m *mockTaskStore) GetDependencies(ctx context.Context, id string) ([]string, error) {
	return nil, nil
}

func (m *mockTaskStore) GetDependents(ctx context.Context, id string) ([]string, error) {
	return nil, nil
}

func (m *mockTaskStore) GetEdgeSource(ctx context.Context, from, to string) (string, error) {
	return "", nil
}

func (m *mockTaskStore) SetTaskTargets(ctx context.Context, taskID string, targets []dag.TaskTarget) error {
	return nil
}

func (m *mockTaskStore) GetTaskTargets(ctx context.Context, taskID string) ([]dag.TaskTarget, error) {
	return nil, nil
}

func (m *mockTaskStore) SetGlobalPaused(ctx context.Context, paused bool) error {
	m.globalPaused = paused
	m.globalPauseSet = true
	return nil
}

func (m *mockTaskStore) IsGlobalPaused(ctx context.Context) (bool, error) {
	return m.globalPaused, m.globalPauseErr
}

func (m *mockTaskStore) IsGlobalPauseSet(ctx context.Context) (bool, bool, error) {
	return m.globalPaused, m.globalPauseSet, m.globalPauseErr
}

func (m *mockTaskStore) GetAllTargetsForStatuses(ctx context.Context, project string, statuses ...string) (map[string][]dag.TaskTarget, error) {
	return nil, nil
}

// mockDescriber implements WorkflowDescriber for testing zombie recovery.
type mockDescriber struct {
	// responses maps workflowID to (response, error) pairs.
	responses map[string]describeResult
}

type describeResult struct {
	resp *workflowservice.DescribeWorkflowExecutionResponse
	err  error
}

func (m *mockDescriber) DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	r, ok := m.responses[workflowID]
	if !ok {
		return nil, fmt.Errorf("workflow %q not found", workflowID)
	}
	return r.resp, r.err
}

func TestScanCandidatesActivity(t *testing.T) {
	tests := []struct {
		name              string
		config            *config.Config
		tasks             map[string][]dag.Task
		expectedCount     int
		expectedProjects  []string
		checkPrompts      bool
		checkNoAcceptance bool
	}{
		{
			name: "empty - no tasks",
			config: &config.Config{
				General: config.General{
					MaxConcurrent: 5,
					ExecTimeout:   config.Duration{Duration: 45 * time.Minute},
					ShortTimeout:  config.Duration{Duration: 2 * time.Minute},
					ReviewTimeout: config.Duration{Duration: 10 * time.Minute},
				},
				Projects: map[string]config.Project{
					"enabled-project": {
						Enabled:   true,
						Workspace: "/tmp/enabled-project",
					},
				},
				Providers: map[string]config.Provider{
					"claude": {
						CLI:     "claude",
						Model:   "claude-sonnet",
						Enabled: true,
						Tier:    "fast",
					},
				},
			},
			tasks:            map[string][]dag.Task{},
			expectedCount:    0,
			expectedProjects: []string{},
		},
		{
			name: "single task",
			config: &config.Config{
				General: config.General{
					MaxConcurrent: 5,
					ExecTimeout:   config.Duration{Duration: 45 * time.Minute},
					ShortTimeout:  config.Duration{Duration: 2 * time.Minute},
					ReviewTimeout: config.Duration{Duration: 10 * time.Minute},
				},
				Projects: map[string]config.Project{
					"enabled-project": {
						Enabled:   true,
						Workspace: "/tmp/enabled-project",
					},
				},
				Providers: map[string]config.Provider{
					"claude": {
						CLI:     "claude",
						Model:   "claude-sonnet",
						Enabled: true,
						Tier:    "fast",
					},
				},
			},
			tasks: map[string][]dag.Task{
				"enabled-project": {
					{
						ID:              "task-1",
						Description:     "Fix the bug",
						EstimateMinutes: 30,
						Acceptance:      "Bug is fixed and tests pass",
						ParentID:        "",
					},
				},
			},
			expectedCount:    1,
			expectedProjects: []string{"enabled-project"},
			checkPrompts:     true,
		},
		{
			name: "max+1 tasks capped at MaxConcurrent",
			config: &config.Config{
				General: config.General{
					MaxConcurrent: 2,
					ExecTimeout:   config.Duration{Duration: 45 * time.Minute},
					ShortTimeout:  config.Duration{Duration: 2 * time.Minute},
					ReviewTimeout: config.Duration{Duration: 10 * time.Minute},
				},
				Projects: map[string]config.Project{
					"enabled-project": {
						Enabled:   true,
						Workspace: "/tmp/enabled-project",
					},
				},
				Providers: map[string]config.Provider{
					"claude": {
						CLI:     "claude",
						Model:   "claude-sonnet",
						Enabled: true,
						Tier:    "fast",
					},
				},
			},
			tasks: map[string][]dag.Task{
				"enabled-project": {
					{
						ID:              "task-1",
						Description:     "First task",
						EstimateMinutes: 30,
						ParentID:        "",
					},
					{
						ID:              "task-2",
						Description:     "Second task",
						EstimateMinutes: 30,
						ParentID:        "",
					},
					{
						ID:              "task-3",
						Description:     "Third task should be dropped",
						EstimateMinutes: 30,
						ParentID:        "",
					},
				},
			},
			expectedCount:    2, // Should be capped at MaxConcurrent
			expectedProjects: []string{"enabled-project"},
		},
		{
			name: "disabled project skipped",
			config: &config.Config{
				General: config.General{
					MaxConcurrent: 5,
					ExecTimeout:   config.Duration{Duration: 45 * time.Minute},
					ShortTimeout:  config.Duration{Duration: 2 * time.Minute},
					ReviewTimeout: config.Duration{Duration: 10 * time.Minute},
				},
				Projects: map[string]config.Project{
					"enabled-project": {
						Enabled:   true,
						Workspace: "/tmp/enabled-project",
					},
					"disabled-project": {
						Enabled:   false,
						Workspace: "/tmp/disabled-project",
					},
				},
				Providers: map[string]config.Provider{
					"claude": {
						CLI:     "claude",
						Model:   "claude-sonnet",
						Enabled: true,
						Tier:    "fast",
					},
				},
			},
			tasks: map[string][]dag.Task{
				"enabled-project": {
					{
						ID:              "task-1",
						Description:     "Enabled task",
						EstimateMinutes: 30,
						ParentID:        "",
					},
				},
				"disabled-project": {
					{
						ID:              "task-2",
						Description:     "Disabled task should not appear",
						EstimateMinutes: 30,
						ParentID:        "",
					},
				},
			},
			expectedCount:    1, // Only from enabled project
			expectedProjects: []string{"enabled-project"},
		},
		{
			name: "task without acceptance criteria",
			config: &config.Config{
				General: config.General{
					MaxConcurrent: 5,
					ExecTimeout:   config.Duration{Duration: 45 * time.Minute},
					ShortTimeout:  config.Duration{Duration: 2 * time.Minute},
					ReviewTimeout: config.Duration{Duration: 10 * time.Minute},
				},
				Projects: map[string]config.Project{
					"enabled-project": {
						Enabled:   true,
						Workspace: "/tmp/enabled-project",
					},
				},
				Providers: map[string]config.Provider{
					"claude": {
						CLI:     "claude",
						Model:   "claude-sonnet",
						Enabled: true,
						Tier:    "fast",
					},
				},
			},
			tasks: map[string][]dag.Task{
				"enabled-project": {
					{
						ID:              "task-no-acceptance",
						Description:     "Simple task without acceptance criteria",
						EstimateMinutes: 30,
						Acceptance:      "", // Empty acceptance criteria
						ParentID:        "",
					},
				},
			},
			expectedCount:     1,
			expectedProjects:  []string{"enabled-project"},
			checkNoAcceptance: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := &mockTaskStore{tasks: tt.tasks}
			da := &DispatchActivities{
				DAG:    mockStore,
				Config: tt.config,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}

			candidates, err := da.ScanCandidatesActivity(context.Background())
			if err != nil {
				t.Fatalf("ScanCandidatesActivity failed: %v", err)
			}

			if len(candidates) != tt.expectedCount {
				t.Errorf("expected %d candidates, got %d", tt.expectedCount, len(candidates))
			}

			// Check that only expected projects appear
			projectsSeen := make(map[string]bool)
			for _, candidate := range candidates {
				projectsSeen[candidate.Project] = true
			}

			for _, expectedProject := range tt.expectedProjects {
				if !projectsSeen[expectedProject] {
					t.Errorf("expected project %q not found in candidates", expectedProject)
				}
			}

			// Verify acceptance criteria are included in prompt when present
			if tt.checkPrompts {
				for _, candidate := range candidates {
					// Should contain task description
					if !strings.Contains(candidate.Prompt, "Fix the bug") {
						t.Errorf("expected task description in prompt, got: %q", candidate.Prompt)
					}
					// Should contain acceptance criteria
					if !strings.Contains(candidate.Prompt, "Bug is fixed and tests pass") {
						t.Errorf("expected acceptance criteria in prompt, got: %q", candidate.Prompt)
					}
					// Should have acceptance criteria header
					if !strings.Contains(candidate.Prompt, "Acceptance Criteria:") {
						t.Errorf("expected acceptance criteria header in prompt, got: %q", candidate.Prompt)
					}
				}
			}

			// Verify no acceptance criteria when empty
			if tt.checkNoAcceptance {
				for _, candidate := range candidates {
					// Should contain task description
					if !strings.Contains(candidate.Prompt, "Simple task without acceptance criteria") {
						t.Errorf("expected task description in prompt, got: %q", candidate.Prompt)
					}
					// Should NOT contain acceptance criteria header
					if strings.Contains(candidate.Prompt, "Acceptance Criteria:") {
						t.Errorf("did not expect acceptance criteria header in prompt, got: %q", candidate.Prompt)
					}
				}
			}

			// Verify basic field population
			for _, candidate := range candidates {
				if candidate.TaskID == "" {
					t.Error("TaskID should not be empty")
				}
				if candidate.Project == "" {
					t.Error("Project should not be empty")
				}
				if candidate.Prompt == "" {
					t.Error("Prompt should not be empty")
				}
				if candidate.WorkDir == "" {
					t.Error("WorkDir should not be empty")
				}
				if candidate.Agent == "" {
					t.Error("Agent should not be empty")
				}
			}
		})
	}
}

func TestScanCandidatesActivity_GlobalPauseSkipsCandidates(t *testing.T) {
	t.Parallel()

	mockStore := &mockTaskStore{
		tasks: map[string][]dag.Task{
			"proj": {
				{ID: "task-1", Description: "should be skipped"},
			},
		},
		globalPaused:   true,
		globalPauseSet: true,
	}
	da := &DispatchActivities{
		DAG: mockStore,
		Config: &config.Config{
			Projects: map[string]config.Project{
				"proj": {Enabled: true, Workspace: "/tmp/proj"},
			},
			Providers: map[string]config.Provider{
				"claude": {CLI: "claude", Model: "sonnet", Enabled: true, Tier: "balanced"},
			},
			Tiers: config.Tiers{Balanced: []string{"claude"}},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	candidates, err := da.ScanCandidatesActivity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates while paused, got %d", len(candidates))
	}
}

func TestPickProvider_PerfInformed(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "perf-test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	if err := perf.Migrate(s.DB()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tracker := perf.New(s.DB(), 0)
	ctx := context.Background()

	// Record history: gemini succeeds often, claude fails often in "fast" tier.
	for i := 0; i < 10; i++ {
		_ = tracker.Record(ctx, "gemini", "flash", "fast", true, 5.0)
		_ = tracker.Record(ctx, "claude", "haiku", "fast", false, 10.0)
	}

	// Verify directly via perf.Pick (avoids needing Temporal activity context).
	p, err := tracker.Pick(ctx, "fast")
	if err != nil {
		t.Fatalf("perf.Pick failed: %v", err)
	}
	if p == nil {
		t.Fatal("expected perf to return a provider, got nil")
	}
	if p.Agent != "gemini" {
		t.Errorf("expected perf to pick gemini (higher success), got %q", p.Agent)
	}
	if p.Model != "flash" {
		t.Errorf("expected model flash, got %q", p.Model)
	}
}

func TestPickProvider_FallbackToConfig(t *testing.T) {
	t.Parallel()

	da := &DispatchActivities{
		Config: &config.Config{
			Providers: map[string]config.Provider{
				"claude": {CLI: "claude", Model: "sonnet", Enabled: true, Tier: "balanced"},
			},
			Tiers: config.Tiers{
				Balanced: []string{"claude"},
			},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Perf:   nil,
	}

	agent, _, tier := da.pickProvider(context.Background(), "balanced")
	if agent != "claude" {
		t.Errorf("expected config fallback to claude, got %q", agent)
	}
	if tier != "balanced" {
		t.Errorf("expected tier balanced, got %q", tier)
	}
}

func TestScanZombieRunningActivity_WorkflowNotFound(t *testing.T) {
	t.Parallel()

	mockStore := &mockTaskStore{
		tasks: map[string][]dag.Task{
			"proj": {
				{ID: "zombie-1", Status: string(types.StatusRunning)},
				{ID: "zombie-2", Status: string(types.StatusRunning)},
			},
		},
		statusUpdates: make(map[string]string),
	}

	// All workflows not found (dead).
	describer := &mockDescriber{responses: map[string]describeResult{}}

	da := &DispatchActivities{
		DAG: mockStore,
		Config: &config.Config{
			Projects: map[string]config.Project{
				"proj": {Enabled: true},
			},
		},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Temporal: describer,
	}

	recovered, err := da.ScanZombieRunningActivity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recovered != 2 {
		t.Errorf("recovered = %d, want 2", recovered)
	}
	for _, id := range []string{"zombie-1", "zombie-2"} {
		if mockStore.statusUpdates[id] != string(types.StatusReady) {
			t.Errorf("task %s status = %q, want %q", id, mockStore.statusUpdates[id], types.StatusReady)
		}
	}
}

func TestScanZombieRunningActivity_WorkflowTerminal(t *testing.T) {
	t.Parallel()

	mockStore := &mockTaskStore{
		tasks: map[string][]dag.Task{
			"proj": {
				{ID: "terminal-1", Status: string(types.StatusRunning)},
			},
		},
		statusUpdates: make(map[string]string),
	}

	describer := &mockDescriber{responses: map[string]describeResult{
		"chum-agent-terminal-1": {
			resp: &workflowservice.DescribeWorkflowExecutionResponse{
				WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
					Status: enums.WORKFLOW_EXECUTION_STATUS_FAILED,
				},
			},
		},
	}}

	da := &DispatchActivities{
		DAG: mockStore,
		Config: &config.Config{
			Projects: map[string]config.Project{
				"proj": {Enabled: true},
			},
		},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Temporal: describer,
	}

	recovered, err := da.ScanZombieRunningActivity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recovered != 1 {
		t.Errorf("recovered = %d, want 1", recovered)
	}
	if mockStore.statusUpdates["terminal-1"] != string(types.StatusReady) {
		t.Errorf("task status = %q, want %q", mockStore.statusUpdates["terminal-1"], types.StatusReady)
	}
}

func TestScanZombieRunningActivity_GlobalPauseMovesToNeedsReview(t *testing.T) {
	t.Parallel()

	mockStore := &mockTaskStore{
		tasks: map[string][]dag.Task{
			"proj": {
				{ID: "zombie-paused", Status: string(types.StatusRunning)},
			},
		},
		statusUpdates:  make(map[string]string),
		globalPaused:   true,
		globalPauseSet: true,
	}
	describer := &mockDescriber{responses: map[string]describeResult{}}

	da := &DispatchActivities{
		DAG: mockStore,
		Config: &config.Config{
			Projects: map[string]config.Project{
				"proj": {Enabled: true},
			},
		},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Temporal: describer,
	}

	recovered, err := da.ScanZombieRunningActivity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	if got := mockStore.statusUpdates["zombie-paused"]; got != string(types.StatusNeedsReview) {
		t.Fatalf("task status = %q, want %q", got, types.StatusNeedsReview)
	}
}

func TestScanZombieRunningActivity_WorkflowStillRunning(t *testing.T) {
	t.Parallel()

	mockStore := &mockTaskStore{
		tasks: map[string][]dag.Task{
			"proj": {
				{ID: "alive-1", Status: string(types.StatusRunning)},
			},
		},
		statusUpdates: make(map[string]string),
	}

	describer := &mockDescriber{responses: map[string]describeResult{
		"chum-agent-alive-1": {
			resp: &workflowservice.DescribeWorkflowExecutionResponse{
				WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
					Status: enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
				},
			},
		},
	}}

	da := &DispatchActivities{
		DAG: mockStore,
		Config: &config.Config{
			Projects: map[string]config.Project{
				"proj": {Enabled: true},
			},
		},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Temporal: describer,
	}

	recovered, err := da.ScanZombieRunningActivity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recovered != 0 {
		t.Errorf("recovered = %d, want 0 (workflow still running)", recovered)
	}
	if _, ok := mockStore.statusUpdates["alive-1"]; ok {
		t.Error("should not have updated status for still-running workflow")
	}
}

func TestScanZombieRunningActivity_NilTemporal(t *testing.T) {
	t.Parallel()

	da := &DispatchActivities{
		DAG:      &mockTaskStore{},
		Config:   &config.Config{},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Temporal: nil, // no Temporal client
	}

	recovered, err := da.ScanZombieRunningActivity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recovered != 0 {
		t.Errorf("recovered = %d, want 0 (nil Temporal)", recovered)
	}
}

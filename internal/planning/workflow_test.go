package planning

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// MockBeadsClient implements BeadsClient interface for testing
type MockBeadsClient struct {
	mock.Mock
}

func (m *MockBeadsClient) Show(ctx context.Context, issueID string) (beads.Issue, error) {
	args := m.Called(ctx, issueID)
	return args.Get(0).(beads.Issue), args.Error(1)
}

// MockLLMClient implements LLMClient interface for testing
type MockLLMClient struct {
	mock.Mock
}

func (m *MockLLMClient) Generate(ctx context.Context, prompt string, model string) (string, error) {
	args := m.Called(ctx, prompt, model)
	return args.String(0), args.Error(1)
}

// TestPlanningCeremonyE2E tests the complete planning workflow through all phases
func TestPlanningCeremonyE2E(t *testing.T) {
	// Create test DAG
	db, err := sql.Open("sqlite", ":memory:")
	assert.NoError(t, err)
	defer db.Close()

	testDAG := dag.NewDAG(db)
	err = testDAG.EnsureSchema(context.Background())
	assert.NoError(t, err)

	// Create mocks
	mockBeads := new(MockBeadsClient)
	mockLLM := new(MockLLMClient)

	// Setup test data
	testIssue := beads.Issue{
		ID:                 "test-issue-123",
		Title:              "Add user authentication system",
		Description:        "Implement JWT-based authentication with login/logout endpoints",
		Status:             "ready",
		Priority:           1,
		IssueType:          "feature",
		Labels:             []string{"backend", "security"},
		AcceptanceCriteria: "Users can login and logout securely. JWT tokens expire after 24 hours.",
		EstimatedMinutes:   180,
	}

	// Mock beads client to return test issue
	mockBeads.On("Show", mock.Anything, "test-issue-123").Return(testIssue, nil)

	// Mock LLM responses for each phase
	clarifyResponse, _ := json.Marshal(ClarifyResult{
		ClarifiedRequirements: "Implement JWT authentication with secure login/logout, password hashing, and token refresh",
		Questions:             []string{"Should we support multi-factor authentication?"},
		EstimatedComplexity:   "Medium",
	})

	researchResponse, _ := json.Marshal(ResearchResult{
		CodebaseAnalysis: "Need to modify auth middleware, add user model, create JWT utilities",
		RelevantFiles:    []string{"internal/auth/", "internal/middleware/auth.go", "internal/models/user.go"},
		Dependencies:     []string{"github.com/golang-jwt/jwt", "golang.org/x/crypto"},
		RiskAssessment:   "Medium risk - security critical component",
	})

	selectResponse, _ := json.Marshal(SelectResult{
		SelectedApproach:   "JWT with bcrypt password hashing and middleware-based auth",
		AlternativeOptions: []string{"Session-based auth", "OAuth integration"},
		Rationale:         "JWT provides stateless auth suitable for microservices",
		ImplementationPlan: "1. User model 2. Auth service 3. Middleware 4. Endpoints",
	})

	decomposeResponse, _ := json.Marshal(DecomposeResult{
		Subtasks: []SubtaskSpec{
			{
				Title:              "Create User model with password hashing",
				Description:        "Implement User struct with bcrypt password hashing methods",
				AcceptanceCriteria: "User can be created with hashed password, passwords can be verified",
				EstimateMinutes:    30,
				Priority:           1,
				Labels:             []string{"model", "security"},
			},
			{
				Title:              "Implement JWT token service",
				Description:        "Create service for generating and validating JWT tokens",
				AcceptanceCriteria: "Tokens can be generated for users and validated",
				EstimateMinutes:    45,
				Priority:           2,
				Labels:             []string{"service", "jwt"},
			},
			{
				Title:              "Add authentication middleware",
				Description:        "Create middleware to protect routes with JWT verification",
				AcceptanceCriteria: "Protected routes require valid JWT token",
				EstimateMinutes:    30,
				Priority:           3,
				Labels:             []string{"middleware", "auth"},
			},
			{
				Title:              "Create login/logout endpoints",
				Description:        "HTTP handlers for user login and logout operations",
				AcceptanceCriteria: "POST /login returns JWT, POST /logout invalidates token",
				EstimateMinutes:    45,
				Priority:           4,
				Labels:             []string{"handlers", "endpoints"},
			},
		},
		Timeline: "3-4 hours total implementation time",
	})

	mockLLM.On("Generate", mock.Anything, mock.MatchedBy(func(prompt string) bool {
		return strings.Contains(prompt, "Analyze this issue and clarify")
	}), "claude-3-sonnet").Return(string(clarifyResponse), nil)

	mockLLM.On("Generate", mock.Anything, mock.MatchedBy(func(prompt string) bool {
		return strings.Contains(prompt, "Research the codebase")
	}), "claude-3-sonnet").Return(string(researchResponse), nil)

	mockLLM.On("Generate", mock.Anything, mock.MatchedBy(func(prompt string) bool {
		return strings.Contains(prompt, "select the best implementation approach")
	}), "claude-3-sonnet").Return(string(selectResponse), nil)

	mockLLM.On("Generate", mock.Anything, mock.MatchedBy(func(prompt string) bool {
		return strings.Contains(prompt, "Decompose this work into specific subtasks")
	}), "claude-3-sonnet").Return(string(decomposeResponse), nil)

	// Setup Temporal test environment
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities with mocked dependencies
	activities := &Activities{
		DAG:         testDAG,
		BeadsClient: mockBeads,
		LLMClient:   mockLLM,
		Logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	env.RegisterActivity(activities.LoadIssueActivity)
	env.RegisterActivity(activities.ClarifyActivity)
	env.RegisterActivity(activities.ResearchActivity)
	env.RegisterActivity(activities.SelectActivity)
	env.RegisterActivity(activities.DecomposeActivity)
	env.RegisterActivity(activities.CreateSubtasksActivity)
	env.RegisterActivity(activities.RecordPlanningDecisionActivity)

	// Setup signals to be sent during workflow execution
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("planning-signal", PlanningSignal{
			Phase:    PhaseResearch,
			Decision: "proceed",
		})
	}, time.Millisecond*10)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("planning-signal", PlanningSignal{
			Phase:    PhaseSelect,
			Decision: "proceed",
		})
	}, time.Millisecond*20)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("planning-signal", PlanningSignal{
			Phase:    PhaseGreenlight,
			Decision: "approved",
		})
	}, time.Millisecond*30)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("planning-signal", PlanningSignal{
			Phase:    PhaseDecompose,
			Decision: "proceed",
		})
	}, time.Millisecond*40)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("planning-signal", PlanningSignal{
			Phase:    PhaseApprove,
			Decision: "approved",
		})
	}, time.Millisecond*50)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("planning-signal", PlanningSignal{
			Phase:    PhaseHandoff,
			Decision: "proceed",
		})
	}, time.Millisecond*60)

	// Start the workflow
	req := PlanningRequest{
		IssueID:  "test-issue-123",
		Project:  "test-project",
		WorkDir:  "/tmp/test-workspace",
		LLMAgent: "claude",
		LLMModel: "claude-3-sonnet",
	}

	env.ExecuteWorkflow(PlanningWorkflow, req)

	// Verify workflow completed successfully
	err = env.GetWorkflowError()
	assert.NoError(t, err, "Workflow should complete without error")

	// Verify all mocks were called as expected
	mockBeads.AssertExpectations(t)
	mockLLM.AssertExpectations(t)

	// Verify subtasks were created in the DAG
	ctx := context.Background()
	tasks, err := testDAG.ListTasks(ctx, "test-project")
	assert.NoError(t, err)

	// Should have created 4 subtasks
	subtaskCount := 0
	var createdTasks []dag.Task
	for _, task := range tasks {
		if task.Type == "subtask" && task.ParentID == "test-issue-123" {
			subtaskCount++
			createdTasks = append(createdTasks, task)
		}
	}

	assert.Equal(t, 4, subtaskCount, "Should have created 4 subtasks")

	// Verify the subtasks have expected properties
	expectedTitles := []string{
		"Create User model with password hashing",
		"Implement JWT token service",
		"Add authentication middleware",
		"Create login/logout endpoints",
	}

	actualTitles := make([]string, len(createdTasks))
	for i, task := range createdTasks {
		actualTitles[i] = task.Title

		// Verify task properties
		assert.Equal(t, "test-project", task.Project)
		assert.Equal(t, "subtask", task.Type)
		assert.Equal(t, "test-issue-123", task.ParentID)
		assert.Equal(t, "pending", task.Status)
		assert.Greater(t, task.EstimateMinutes, 0)
		assert.NotEmpty(t, task.Description)
		assert.NotEmpty(t, task.Acceptance)
	}

	// Verify all expected subtasks were created (order may vary)
	for _, expectedTitle := range expectedTitles {
		assert.Contains(t, actualTitles, expectedTitle, "Expected subtask should be created: %s", expectedTitle)
	}

	// Verify planning decision was recorded (check activities were called)
	// This is implicitly verified by the workflow completing successfully
	// and the mocks asserting their expectations

	t.Logf("Planning ceremony E2E test completed successfully:")
	t.Logf("- Issue processed: %s", testIssue.ID)
	t.Logf("- Subtasks created: %d", subtaskCount)
	t.Logf("- All phases completed: clarify -> research -> select -> greenlight -> decompose -> approve -> handoff")

}
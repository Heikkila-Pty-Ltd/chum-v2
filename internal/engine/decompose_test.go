package engine

import (
	"context"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
	"go.temporal.io/sdk/testsuite"
)

func TestBuildDecompPrompt_ContainsTask(t *testing.T) {
	prompt := buildDecompPrompt("Add a health endpoint", "Go packages:\nmain")
	if len(prompt) == 0 {
		t.Fatal("prompt is empty")
	}
	if !contains(prompt, "Add a health endpoint") {
		t.Error("prompt should contain task description")
	}
	if !contains(prompt, "Go packages") {
		t.Error("prompt should contain codebase context")
	}
}

func TestDecompResult_EmptyStepsIsAtomic(t *testing.T) {
	r := types.DecompResult{Steps: nil}
	if len(r.Steps) != 0 {
		t.Error("expected no steps")
	}
}

func TestDecompResult_WithSteps(t *testing.T) {
	r := types.DecompResult{
		Steps: []types.DecompStep{
			{Title: "Step 1", Description: "Do thing 1", Acceptance: "Thing 1 done", Estimate: 15},
			{Title: "Step 2", Description: "Do thing 2", Acceptance: "Thing 2 done", Estimate: 30},
		},
	}
	if len(r.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(r.Steps))
	}
	if r.Steps[0].Estimate != 15 {
		t.Errorf("expected estimate 15, got %d", r.Steps[0].Estimate)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// mockLLM implements llm.Runner for testing
type mockLLM struct {
	planOutput string
	execOutput string
	err        error
}

func (m *mockLLM) Plan(ctx context.Context, agent, model, workDir, prompt string) (*llm.CLIResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.CLIResult{ExitCode: 0, Output: m.planOutput}, nil
}

func (m *mockLLM) Exec(ctx context.Context, agent, model, workDir, prompt string) (*llm.CLIResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.CLIResult{ExitCode: 0, Output: m.execOutput}, nil
}

func TestDecomposeActivity_ValidJSONWith3Steps(t *testing.T) {
	t.Parallel()

	// Mock LLM that returns valid JSON with 3 steps
	mockLLMRunner := &mockLLM{
		planOutput: `{
			"steps": [
				{
					"title": "Step 1",
					"description": "Implement feature A",
					"acceptance": "Feature A works correctly",
					"estimate_minutes": 30
				},
				{
					"title": "Step 2",
					"description": "Add tests for feature A",
					"acceptance": "Tests pass and coverage is adequate",
					"estimate_minutes": 20
				},
				{
					"title": "Step 3",
					"description": "Update documentation",
					"acceptance": "Documentation is updated and reviewed",
					"estimate_minutes": 10
				}
			]
		}`,
	}

	activities := &Activities{
		LLM: mockLLMRunner,
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(activities.DecomposeActivity)

	req := TaskRequest{
		TaskID:  "test-task",
		Project: "test-project",
		WorkDir: t.TempDir(),
		Agent:   "test-agent",
		Model:   "test-model",
		Prompt:  "Add a new feature with tests and docs",
	}

	result, err := env.ExecuteActivity(activities.DecomposeActivity, req)
	if err != nil {
		t.Fatalf("DecomposeActivity failed: %v", err)
	}

	var decompResult *types.DecompResult
	err = result.Get(&decompResult)
	if err != nil {
		t.Fatalf("Failed to get result: %v", err)
	}

	// Verify returned DecompResult has Atomic=false and 3 steps with correct fields
	if decompResult.Atomic {
		t.Error("Expected Atomic to be false for decomposed task")
	}

	if len(decompResult.Steps) != 3 {
		t.Fatalf("Expected 3 steps, got %d", len(decompResult.Steps))
	}

	// Verify first step
	step1 := decompResult.Steps[0]
	if step1.Title != "Step 1" {
		t.Errorf("Expected step 1 title 'Step 1', got '%s'", step1.Title)
	}
	if step1.Description != "Implement feature A" {
		t.Errorf("Expected step 1 description 'Implement feature A', got '%s'", step1.Description)
	}
	if step1.Acceptance != "Feature A works correctly" {
		t.Errorf("Expected step 1 acceptance 'Feature A works correctly', got '%s'", step1.Acceptance)
	}
	if step1.Estimate != 30 {
		t.Errorf("Expected step 1 estimate 30, got %d", step1.Estimate)
	}

	// Verify second step
	step2 := decompResult.Steps[1]
	if step2.Title != "Step 2" {
		t.Errorf("Expected step 2 title 'Step 2', got '%s'", step2.Title)
	}
	if step2.Estimate != 20 {
		t.Errorf("Expected step 2 estimate 20, got %d", step2.Estimate)
	}

	// Verify third step
	step3 := decompResult.Steps[2]
	if step3.Title != "Step 3" {
		t.Errorf("Expected step 3 title 'Step 3', got '%s'", step3.Title)
	}
	if step3.Estimate != 10 {
		t.Errorf("Expected step 3 estimate 10, got %d", step3.Estimate)
	}
}

func TestDecomposeActivity_EmptyStepsJSON_AtomicTrue(t *testing.T) {
	t.Parallel()

	// Mock LLM that returns empty steps JSON
	mockLLMRunner := &mockLLM{
		planOutput: `{"steps": []}`,
	}

	activities := &Activities{
		LLM: mockLLMRunner,
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(activities.DecomposeActivity)

	req := TaskRequest{
		TaskID:  "test-atomic-task",
		Project: "test-project",
		WorkDir: t.TempDir(),
		Agent:   "test-agent",
		Model:   "test-model",
		Prompt:  "Fix a simple typo",
	}

	result, err := env.ExecuteActivity(activities.DecomposeActivity, req)
	if err != nil {
		t.Fatalf("DecomposeActivity failed: %v", err)
	}

	var decompResult *types.DecompResult
	err = result.Get(&decompResult)
	if err != nil {
		t.Fatalf("Failed to get result: %v", err)
	}

	// Verify Atomic=true for empty steps
	if !decompResult.Atomic {
		t.Error("Expected Atomic to be true for empty steps")
	}

	if len(decompResult.Steps) != 0 {
		t.Fatalf("Expected 0 steps, got %d", len(decompResult.Steps))
	}
}

package engine

import (
	"context"
	"errors"
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

// mockLLM allows testing different LLM response scenarios
type mockLLM struct {
	planErr    error
	planResult *llm.CLIResult
}

func (m *mockLLM) Plan(ctx context.Context, agent, model, workDir, prompt string) (*llm.CLIResult, error) {
	return m.planResult, m.planErr
}

func (m *mockLLM) Exec(ctx context.Context, agent, model, workDir, prompt string) (*llm.CLIResult, error) {
	return nil, errors.New("not implemented for test")
}

func TestDecomposeActivity_LLMPlanError(t *testing.T) {
	// Test case 1: LLM.Plan returns error
	activities := &Activities{
		LLM: &mockLLM{
			planErr: errors.New("LLM plan failed"),
		},
	}

	req := TaskRequest{
		TaskID:  "test-task",
		Prompt:  "Add feature",
		WorkDir: "/tmp/test",
		Agent:   "test-agent",
		Model:   "test-model",
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(activities.DecomposeActivity)

	val, err := env.ExecuteActivity(activities.DecomposeActivity, req)

	if err == nil {
		t.Fatal("expected error when LLM.Plan fails, got nil")
	}
	if val != nil {
		t.Errorf("expected nil result when LLM.Plan fails, got %+v", val)
	}
	if !contains(err.Error(), "decompose CLI") {
		t.Errorf("expected error to mention 'decompose CLI', got: %s", err.Error())
	}
}

func TestDecomposeActivity_LLMNonZeroExitCode(t *testing.T) {
	// Test case 2: LLM returns non-zero exit code
	activities := &Activities{
		LLM: &mockLLM{
			planResult: &llm.CLIResult{
				ExitCode: 1,
				Output:   "Command failed with some error",
			},
		},
	}

	req := TaskRequest{
		TaskID:  "test-task",
		Prompt:  "Add feature",
		WorkDir: "/tmp/test",
		Agent:   "test-agent",
		Model:   "test-model",
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(activities.DecomposeActivity)

	val, err := env.ExecuteActivity(activities.DecomposeActivity, req)

	if err == nil {
		t.Fatal("expected error when LLM returns non-zero exit code, got nil")
	}
	if val != nil {
		t.Errorf("expected nil result when LLM returns non-zero exit code, got %+v", val)
	}
	if !contains(err.Error(), "decompose CLI exited 1") {
		t.Errorf("expected error to mention 'decompose CLI exited 1', got: %s", err.Error())
	}
}

func TestDecomposeActivity_LLMInvalidJSON(t *testing.T) {
	// Test case 3: LLM returns output with no valid JSON (should fallback to Atomic=true)
	activities := &Activities{
		LLM: &mockLLM{
			planResult: &llm.CLIResult{
				ExitCode: 0,
				Output:   "This is some text without any valid JSON structure",
			},
		},
	}

	req := TaskRequest{
		TaskID:  "test-task",
		Prompt:  "Add feature",
		WorkDir: "/tmp/test",
		Agent:   "test-agent",
		Model:   "test-model",
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(activities.DecomposeActivity)

	val, err := env.ExecuteActivity(activities.DecomposeActivity, req)

	if err != nil {
		t.Fatalf("expected no error when LLM returns invalid JSON, got: %s", err.Error())
	}

	var result types.DecompResult
	if err := val.Get(&result); err != nil {
		t.Fatalf("failed to decode activity result: %s", err.Error())
	}

	if !result.Atomic {
		t.Error("expected Atomic=true when LLM returns invalid JSON, got false")
	}
	if len(result.Steps) != 0 {
		t.Errorf("expected empty steps when Atomic=true, got %d steps", len(result.Steps))
	}
}

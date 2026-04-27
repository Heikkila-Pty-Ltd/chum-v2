package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"go.temporal.io/sdk/testsuite"
)

func TestExecuteActivity_PreflightFailsWhenCLIMissing(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Projects: map[string]config.Project{
			"p": {DoDChecks: []string{"go build ./..."}},
		},
	}
	a := &Activities{Config: cfg}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.ExecuteActivity)
	_, err := env.ExecuteActivity(a.ExecuteActivity, TaskRequest{
		TaskID:  "t-1",
		Project: "p",
		WorkDir: t.TempDir(),
		Agent:   "definitely-missing-cli-for-test",
	})
	if err == nil {
		t.Fatal("expected ExecuteActivity preflight error, got nil")
	}
	if !strings.Contains(err.Error(), "PREFLIGHT: CLI") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSetupCommandsActivity_RunsCommands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	a := &Activities{Config: &config.Config{Projects: map[string]config.Project{
		"test": {
			Enabled:       true,
			Workspace:     dir,
			SetupCommands: []string{"touch setup_ran.marker"},
		},
	}}}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.RunSetupCommandsActivity)
	_, err := env.ExecuteActivity(a.RunSetupCommandsActivity, dir, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "setup_ran.marker")); statErr != nil {
		t.Fatalf("setup command did not create marker file: %v", statErr)
	}
}

func TestRunSetupCommandsActivity_NoCommandsIsNoop(t *testing.T) {
	t.Parallel()

	a := &Activities{Config: &config.Config{Projects: map[string]config.Project{
		"test": {Enabled: true},
	}}}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.RunSetupCommandsActivity)
	_, err := env.ExecuteActivity(a.RunSetupCommandsActivity, t.TempDir(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSetupCommandsActivity_UnknownProjectIsNoop(t *testing.T) {
	t.Parallel()

	a := &Activities{Config: &config.Config{Projects: map[string]config.Project{}}}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.RunSetupCommandsActivity)
	_, err := env.ExecuteActivity(a.RunSetupCommandsActivity, t.TempDir(), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildPromptForMode_Research(t *testing.T) {
	t.Parallel()
	prompt := buildPromptForMode("research", "investigate the bug", "func main() {}")
	if !strings.Contains(prompt, "investigating a question") {
		t.Error("research prompt should mention investigating")
	}
	if !strings.Contains(prompt, "Do NOT make code changes") {
		t.Error("research prompt should prohibit code changes")
	}
	if !strings.Contains(prompt, "func main()") {
		t.Error("research prompt should include codebase context")
	}
}

func TestBuildPromptForMode_ResearchNoCodebase(t *testing.T) {
	t.Parallel()
	prompt := buildPromptForMode("research", "research competitor pricing", "")
	if strings.Contains(prompt, "CODEBASE") {
		t.Error("research prompt without codebase should not mention CODEBASE")
	}
}

func TestBuildPromptForMode_CodeChange(t *testing.T) {
	t.Parallel()
	prompt := buildPromptForMode("code_change", "add endpoint", "func main() {}")
	if !strings.Contains(prompt, "Implement") {
		t.Error("code_change prompt should mention Implement")
	}
}

func TestBuildPromptForMode_DefaultIsCodeChange(t *testing.T) {
	t.Parallel()
	prompt := buildPromptForMode("", "do something", "context")
	if !strings.Contains(prompt, "Implement") {
		t.Error("empty mode should default to code_change prompt")
	}
}

func TestRunCommandActivity_RunsCommands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	a := &Activities{Config: &config.Config{}}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.RunCommandActivity)

	var output string
	val, err := env.ExecuteActivity(a.RunCommandActivity, dir, []string{"echo hello", "echo world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := val.Get(&output); err != nil {
		t.Fatalf("get output: %v", err)
	}
	if !strings.Contains(output, "hello") || !strings.Contains(output, "world") {
		t.Errorf("output should contain both commands: %s", output)
	}
}

func TestRunCommandActivity_FailingCommandIncludesError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	a := &Activities{Config: &config.Config{}}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.RunCommandActivity)

	var output string
	val, err := env.ExecuteActivity(a.RunCommandActivity, dir, []string{"false"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := val.Get(&output); err != nil {
		t.Fatalf("get output: %v", err)
	}
	if !strings.Contains(output, "ERROR") {
		t.Errorf("output should contain ERROR for failing command: %s", output)
	}
}

func TestDoDCheckActivity_ProjectNotFound(t *testing.T) {
	t.Parallel()

	a := &Activities{Config: &config.Config{Projects: map[string]config.Project{}}}
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.DoDCheckActivity)
	_, err := env.ExecuteActivity(a.DoDCheckActivity, t.TempDir(), "missing-project")
	if err == nil {
		t.Fatal("expected project-not-found error, got nil")
	}
	if !strings.Contains(err.Error(), `project "missing-project" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

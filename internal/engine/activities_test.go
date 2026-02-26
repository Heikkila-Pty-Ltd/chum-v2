package engine

import (
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
	_, err := env.ExecuteActivity(a.ExecuteActivity, Plan{
		Summary: "x",
		Steps:   []string{"y"},
	}, TaskRequest{
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

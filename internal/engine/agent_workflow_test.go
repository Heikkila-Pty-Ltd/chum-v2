package engine

import (
	"errors"
	"strings"
	"testing"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

func TestAgentWorkflow_ExecFailure_ClosesAndCleans(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-1").Return("/tmp/wt-task-1", nil)
	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything).Return((*ExecResult)(nil), errors.New("exec failed"))
	env.OnActivity(a.CloseTaskActivity, mock.Anything, "task-1", "exec_failed").Return(nil)
	env.OnActivity(a.CleanupWorktreeActivity, mock.Anything, "/repo", "/tmp/wt-task-1").Return(nil)

	env.ExecuteWorkflow(AgentWorkflow, TaskRequest{
		TaskID:  "task-1",
		Project: "p",
		Prompt:  "do thing",
		WorkDir: "/repo",
		Agent:   "claude",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "execute failed") {
		t.Fatalf("expected exec failure error, got %v", err)
	}
}

func TestAgentWorkflow_DoDFailure_ClosesAndCleans(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-2").Return("/tmp/wt-task-2", nil)
	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything).Return(&ExecResult{
		ExitCode: 0,
		Output:   "ok",
	}, nil)
	env.OnActivity(a.DoDCheckActivity, mock.Anything, "/tmp/wt-task-2", "p").Return(&gitpkg.DoDResult{
		Passed:   false,
		Failures: []string{"go test ./... (exit 1)"},
	}, nil)
	env.OnActivity(a.CloseTaskActivity, mock.Anything, "task-2", "dod_failed").Return(nil)
	env.OnActivity(a.CleanupWorktreeActivity, mock.Anything, "/repo", "/tmp/wt-task-2").Return(nil)

	env.ExecuteWorkflow(AgentWorkflow, TaskRequest{
		TaskID:  "task-2",
		Project: "p",
		Prompt:  "do thing",
		WorkDir: "/repo",
		Agent:   "claude",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "DoD failed") {
		t.Fatalf("expected DoD failure error, got %v", err)
	}
}

func TestAgentWorkflow_SuccessPath_Completes(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-3").Return("/tmp/wt-task-3", nil)
	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything).Return(&ExecResult{
		ExitCode: 0,
		Output:   "ok",
	}, nil)
	env.OnActivity(a.DoDCheckActivity, mock.Anything, "/tmp/wt-task-3", "p").Return(&gitpkg.DoDResult{
		Passed: true,
	}, nil)
	env.OnActivity(a.PushActivity, mock.Anything, "/tmp/wt-task-3").Return(nil)
	env.OnActivity(a.CreatePRActivity, mock.Anything, "/tmp/wt-task-3", mock.Anything).Return(nil)
	env.OnActivity(a.CloseTaskActivity, mock.Anything, "task-3", "completed").Return(nil)
	env.OnActivity(a.CleanupWorktreeActivity, mock.Anything, "/repo", "/tmp/wt-task-3").Return(nil)

	env.ExecuteWorkflow(AgentWorkflow, TaskRequest{
		TaskID:  "task-3",
		Project: "p",
		Prompt:  "do thing",
		WorkDir: "/repo",
		Agent:   "claude",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("unexpected workflow error: %v", err)
	}
}

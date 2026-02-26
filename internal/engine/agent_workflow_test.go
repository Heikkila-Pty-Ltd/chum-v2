package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestAgentWorkflow_ExecFailure_ClosesAndCleans(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-1").Return("/tmp/wt-task-1", nil)
	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything).Return((*ExecResult)(nil), errors.New("exec failed"))
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-1", mock.Anything).Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)
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
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-2", mock.Anything).Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)
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
	env.OnActivity(a.CreatePRInfoActivity, mock.Anything, "/tmp/wt-task-3", mock.Anything).Return(&PRInfo{
		Number:  123,
		HeadSHA: "abc123",
		URL:     "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/2",
	}, nil)
	env.OnActivity(a.ResolveReviewerLoginActivity, mock.Anything, "/tmp/wt-task-3").Return("review-bot", nil)
	env.OnActivity(a.RunReviewActivity, mock.Anything, "/tmp/wt-task-3", 123, 1, "claude").Return(&ReviewDraft{
		Signal: "APPROVE",
		Body:   "Looks good.",
	}, nil)
	env.OnActivity(a.GuardReviewerCleanActivity, mock.Anything, "/tmp/wt-task-3").Return(nil)
	env.OnActivity(a.SubmitReviewActivity, mock.Anything, "/tmp/wt-task-3", 123, 1, "review-bot", "abc123", "APPROVE", "Looks good.").Return(&ReviewResult{
		Outcome:   ReviewApproved,
		ReviewURL: "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/2#pullrequestreview-1",
	}, nil)
	env.OnActivity(a.CheckPRStateActivity, mock.Anything, "/tmp/wt-task-3", 123, 1, "review-bot", "abc123").Return(&ReviewResult{
		Outcome:   ReviewApproved,
		ReviewURL: "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/2#pullrequestreview-1",
	}, nil)
	env.OnActivity(a.MergePRActivity, mock.Anything, "/tmp/wt-task-3", 123).Return(&MergeResult{
		Merged: true,
	}, nil)
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-3", mock.Anything).Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)
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

func TestCloseAndNotify_PropagatesCloseFailure(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-close-fail", mock.Anything).Return(errors.New("db unavailable"))
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return closeAndNotify(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}, "task-close-fail", CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "unit_test",
		})
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	err := env.GetWorkflowError()
	if err == nil || !strings.Contains(err.Error(), "close task failed") {
		t.Fatalf("expected close task failure, got %v", err)
	}
}

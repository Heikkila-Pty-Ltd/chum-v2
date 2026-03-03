package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
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
	env.OnActivity(a.DecomposeActivity, mock.Anything, mock.Anything).Return(&types.DecompResult{Atomic: true}, nil)
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
	env.OnActivity(a.DecomposeActivity, mock.Anything, mock.Anything).Return(&types.DecompResult{Atomic: true}, nil)
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
	env.OnActivity(a.DecomposeActivity, mock.Anything, mock.Anything).Return(&types.DecompResult{Atomic: true}, nil)
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

func TestAgentWorkflow_Decomposition_ClosesParent(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-decomp").Return("/tmp/wt-task-decomp", nil)
	env.OnActivity(a.DecomposeActivity, mock.Anything, mock.Anything).Return(&types.DecompResult{
		Steps: []types.DecompStep{
			{Title: "Step 1", Description: "Do thing 1", Acceptance: "Done 1", Estimate: 15},
			{Title: "Step 2", Description: "Do thing 2", Acceptance: "Done 2", Estimate: 30},
		},
	}, nil)
	env.OnActivity(a.CreateSubtasksActivity, mock.Anything, "task-decomp", "proj", mock.Anything).Return([]string{"sub-1", "sub-2"}, nil)
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-decomp", mock.Anything).Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.CleanupWorktreeActivity, mock.Anything, "/repo", "/tmp/wt-task-decomp").Return(nil)

	env.ExecuteWorkflow(AgentWorkflow, TaskRequest{
		TaskID:  "task-decomp",
		Project: "proj",
		Prompt:  "do something vague",
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

func TestAgentWorkflow_DecompFailure_HardFails(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-df").Return("/tmp/wt-task-df", nil)
	env.OnActivity(a.DecomposeActivity, mock.Anything, mock.Anything).Return((*types.DecompResult)(nil), errors.New("LLM unavailable"))
	// No ExecuteActivity mock — decomposition failure must NOT fall through
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-df", mock.Anything).Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.CleanupWorktreeActivity, mock.Anything, "/repo", "/tmp/wt-task-df").Return(nil)

	env.ExecuteWorkflow(AgentWorkflow, TaskRequest{
		TaskID:  "task-df",
		Project: "p",
		Prompt:  "do thing",
		WorkDir: "/repo",
		Agent:   "claude",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "decompose failed") {
		t.Fatalf("expected decompose failure, got %v", err)
	}
}

func TestAgentWorkflow_Subtask_SkipsDecomposition(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-sub").Return("/tmp/wt-task-sub", nil)
	// NO DecomposeActivity mock — it should NOT be called for subtasks
	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything).Return(&ExecResult{ExitCode: 0, Output: "ok"}, nil)
	env.OnActivity(a.DoDCheckActivity, mock.Anything, "/tmp/wt-task-sub", "p").Return(&gitpkg.DoDResult{
		Passed:   false,
		Failures: []string{"test fail"},
	}, nil)
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-sub", mock.Anything).Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.CleanupWorktreeActivity, mock.Anything, "/repo", "/tmp/wt-task-sub").Return(nil)

	env.ExecuteWorkflow(AgentWorkflow, TaskRequest{
		TaskID:   "task-sub",
		Project:  "p",
		Prompt:   "specific subtask",
		WorkDir:  "/repo",
		Agent:    "claude",
		ParentID: "parent-task-1", // This makes it a subtask
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	// Should have executed and hit DoD failure (proving decompose was skipped)
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "DoD failed") {
		t.Fatalf("expected DoD failure (proving decomp skip), got %v", err)
	}
}

func TestAgentWorkflow_ReviewRejectionExhaustsRetries(t *testing.T) {
	t.Parallel()

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	env.OnActivity(a.SetupWorktreeActivity, mock.Anything, "/repo", "task-review-exhausted").Return("/tmp/wt-task-review-exhausted", nil)
	env.OnActivity(a.DecomposeActivity, mock.Anything, mock.Anything).Return(&types.DecompResult{Atomic: true}, nil)
	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything).Return(&ExecResult{
		ExitCode: 0,
		Output:   "ok",
	}, nil)
	env.OnActivity(a.DoDCheckActivity, mock.Anything, "/tmp/wt-task-review-exhausted", "p").Return(&gitpkg.DoDResult{
		Passed: true,
	}, nil)
	env.OnActivity(a.PushActivity, mock.Anything, "/tmp/wt-task-review-exhausted").Return(nil)
	env.OnActivity(a.CreatePRInfoActivity, mock.Anything, "/tmp/wt-task-review-exhausted", mock.Anything).Return(&PRInfo{
		Number:  456,
		HeadSHA: "def456",
		URL:     "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/456",
	}, nil)
	env.OnActivity(a.ResolveReviewerLoginActivity, mock.Anything, "/tmp/wt-task-review-exhausted").Return("review-bot", nil)

	// Round 1: REQUEST_CHANGES
	env.OnActivity(a.RunReviewActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456, 1, "claude").Return(&ReviewDraft{
		Signal: "REQUEST_CHANGES",
		Body:   "Please fix the issues found.",
	}, nil)
	env.OnActivity(a.GuardReviewerCleanActivity, mock.Anything, "/tmp/wt-task-review-exhausted").Return(nil)
	env.OnActivity(a.SubmitReviewActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456, 1, "review-bot", "def456", "REQUEST_CHANGES", "Please fix the issues found.").Return(&ReviewResult{
		Outcome:   ReviewChangesRequested,
		ReviewURL: "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/456#pullrequestreview-1",
		Comments:  "Please fix the issues found.",
		ReviewID:  12345,
	}, nil)
	env.OnActivity(a.CheckPRStateActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456, 1, "review-bot", "def456").Return(&ReviewResult{
		Outcome:   ReviewChangesRequested,
		ReviewURL: "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/456#pullrequestreview-1",
		Comments:  "Please fix the issues found.",
		ReviewID:  12345,
	}, nil)
	env.OnActivity(a.ReadReviewFeedbackActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456, int64(12345)).Return("Inline review feedback", nil)

	// After round 1 changes: re-execute, DoD, push, get PR info
	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything).Return(&ExecResult{
		ExitCode: 0,
		Output:   "fixed",
	}, nil)
	env.OnActivity(a.DoDCheckActivity, mock.Anything, "/tmp/wt-task-review-exhausted", "p").Return(&gitpkg.DoDResult{
		Passed: true,
	}, nil)
	env.OnActivity(a.PushActivity, mock.Anything, "/tmp/wt-task-review-exhausted").Return(nil)
	env.OnActivity(a.GetPRInfoActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456).Return(&PRInfo{
		Number:  456,
		HeadSHA: "ghi789",
		URL:     "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/456",
	}, nil)

	// Round 2: REQUEST_CHANGES (final round)
	env.OnActivity(a.RunReviewActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456, 2, "claude").Return(&ReviewDraft{
		Signal: "REQUEST_CHANGES",
		Body:   "Still needs more work.",
	}, nil)
	env.OnActivity(a.GuardReviewerCleanActivity, mock.Anything, "/tmp/wt-task-review-exhausted").Return(nil)
	env.OnActivity(a.SubmitReviewActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456, 2, "review-bot", "ghi789", "REQUEST_CHANGES", "Still needs more work.").Return(&ReviewResult{
		Outcome:   ReviewChangesRequested,
		ReviewURL: "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/456#pullrequestreview-2",
		Comments:  "Still needs more work.",
		ReviewID:  67890,
	}, nil)
	env.OnActivity(a.CheckPRStateActivity, mock.Anything, "/tmp/wt-task-review-exhausted", 456, 2, "review-bot", "ghi789").Return(&ReviewResult{
		Outcome:   ReviewChangesRequested,
		ReviewURL: "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/456#pullrequestreview-2",
		Comments:  "Still needs more work.",
		ReviewID:  67890,
	}, nil)

	// Final close as needs_review
	env.OnActivity(a.CloseTaskWithDetailActivity, mock.Anything, "task-review-exhausted", CloseDetail{
		Reason:    CloseNeedsReview,
		SubReason: "max_rounds_reached",
		PRNumber:  456,
		ReviewURL: "https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/456#pullrequestreview-2",
	}).Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.CleanupWorktreeActivity, mock.Anything, "/repo", "/tmp/wt-task-review-exhausted").Return(nil)

	env.ExecuteWorkflow(AgentWorkflow, TaskRequest{
		TaskID:  "task-review-exhausted",
		Project: "p",
		Prompt:  "implement feature",
		WorkDir: "/repo",
		Agent:   "claude",
	})

	if !env.IsWorkflowCompleted() {
		t.Fatal("expected workflow completion")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("unexpected workflow error: %v", err)
	}

	// Verify ReadReviewFeedbackActivity was called between rounds
	env.AssertExpectations(t)
}

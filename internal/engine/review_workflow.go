package engine

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ReviewWorkflow resumes the review/merge pipeline for an orphaned PR.
//
// When AgentWorkflow dies after creating a PR but before the review loop
// completes, the task lands in "needs_review" with a PR number but no
// running workflow. ReviewWorkflow picks up from that point:
//
//	GetPRInfo → SetupWorktree(at PR head) → Review Loop → Merge → Close
//
// It resumes from an existing PR and only re-executes when reviewer requests changes.
func ReviewWorkflow(ctx workflow.Context, req ReviewRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("ReviewWorkflow started", "TaskID", req.TaskID, "PR", req.PRNumber)

	var a *Activities
	reviewRoundsVersion := workflow.GetVersion(ctx, "review-configurable-review-rounds", workflow.DefaultVersion, 1)

	// Version gate: trace recording added to ReviewWorkflow.
	traceVersion := workflow.GetVersion(ctx, "review-add-trace-recording", workflow.DefaultVersion, 1)
	startTime := workflow.Now(ctx)

	// --- Activity options ---
	shortTimeout := req.ShortTimeout
	if shortTimeout <= 0 {
		shortTimeout = 2 * time.Minute
	}
	execTimeout := req.ExecTimeout
	if execTimeout <= 0 {
		execTimeout = 45 * time.Minute
	}
	reviewTimeout := req.ReviewTimeout
	if reviewTimeout <= 0 {
		reviewTimeout = 10 * time.Minute
	}
	dodTimeout := execTimeout
	if reviewTimeout > dodTimeout {
		dodTimeout = reviewTimeout
	}

	shortOpts := workflow.ActivityOptions{
		StartToCloseTimeout: shortTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	execOpts := workflow.ActivityOptions{
		StartToCloseTimeout: execTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	reviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: reviewTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	dodOpts := workflow.ActivityOptions{
		StartToCloseTimeout: dodTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	baseWorkDir := req.WorkDir
	// Use SideEffect to record os.TempDir() deterministically for Temporal replay.
	var worktreeTmpDir string
	_ = workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return os.TempDir()
	}).Get(&worktreeTmpDir)
	predictableWorktreePath := filepath.Join(worktreeTmpDir, "chum-worktrees", req.TaskID)
	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		cleanCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(cleanCtx, a.CleanupWorktreeActivity, baseWorkDir, predictableWorktreePath).Get(ctx, nil)
	}
	defer cleanup()

	// closeAndTrace wraps closeAndNotify and records an execution trace
	// (version-gated so pre-existing workflows don't break on replay).
	closeAndTrace := func(detail CloseDetail) error {
		cerr := closeAndNotify(ctx, shortOpts, req.TaskID, detail)
		if traceVersion == 1 {
			traceCtx := workflow.WithActivityOptions(ctx, shortOpts)
			info := workflow.GetInfo(ctx)
			_ = workflow.ExecuteActivity(traceCtx, a.RecordTraceActivity, TraceOutcome{
				TaskID:    req.TaskID,
				SessionID: info.WorkflowExecution.RunID,
				Agent:     req.Agent,
				Model:     req.Model,
				Reason:    string(detail.Reason),
				SubReason: detail.SubReason,
				Duration:  workflow.Now(ctx).Sub(startTime),
			}).Get(ctx, nil)
		}
		return cerr
	}

	// Mark task as running so the dispatcher doesn't double-pick it.
	markCtx := workflow.WithActivityOptions(ctx, shortOpts)
	_ = workflow.ExecuteActivity(markCtx, a.CloseTaskActivity, req.TaskID, "running").Get(ctx, nil)

	// === GET PR INFO ===
	prCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var prInfo PRInfo
	if err := workflow.ExecuteActivity(prCtx, a.GetPRInfoActivity, baseWorkDir, req.PRNumber).Get(ctx, &prInfo); err != nil {
		logger.Error("Failed to get PR info", "error", err)
		return closeAndTrace(CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "pr_info_failed",
			PRNumber:  req.PRNumber,
		})
	}

	// === WORKTREE SETUP (at PR head) ===
	setupStartRef := strings.TrimSpace(prInfo.HeadSHA)
	if setupStartRef == "" {
		setupStartRef = "HEAD"
	}
	var worktreePath string
	if err := workflow.ExecuteActivity(prCtx, a.SetupWorktreeFromRefActivity, baseWorkDir, req.TaskID, setupStartRef).Get(ctx, &worktreePath); err != nil {
		logger.Error("Worktree setup failed for review recovery", "error", err)
		return closeAndTrace(CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "worktree_failed",
			PRNumber:  prInfo.Number,
			ReviewURL: prInfo.URL,
		})
	}
	req.WorkDir = worktreePath
	logger.Info("Worktree isolated for review recovery", "path", worktreePath, "start_ref", setupStartRef)

	// === RESOLVE REVIEWER ===
	var reviewerLogin string
	if err := workflow.ExecuteActivity(prCtx, a.ResolveReviewerLoginActivity, req.WorkDir).Get(ctx, &reviewerLogin); err != nil {
		logger.Error("Reviewer login resolution failed", "error", err)
		return closeAndTrace(CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "reviewer_error",
			PRNumber:  prInfo.Number,
			ReviewURL: prInfo.URL,
		})
	}

	// === REVIEW LOOP (shared with AgentWorkflow) ===
	maxReviewRounds := 2
	if reviewRoundsVersion == 1 && req.MaxReviewRounds > 0 {
		maxReviewRounds = req.MaxReviewRounds
	}

	return reviewLoop(ctx, &reviewLoopParams{
		TaskID:          req.TaskID,
		Project:         req.Project,
		Prompt:          req.Prompt,
		WorkDir:         req.WorkDir,
		Agent:           req.Agent,
		Model:           req.Model,
		MaxReviewRounds: maxReviewRounds,
		PRInfo:          prInfo,
		ReviewerLogin:   reviewerLogin,
		ShortOpts:       shortOpts,
		ExecOpts:        execOpts,
		ReviewOpts:      reviewOpts,
		DoDOpts:         dodOpts,
		CloseFn:         closeAndTrace,
		ClassifyDoD:     false,
	})
}

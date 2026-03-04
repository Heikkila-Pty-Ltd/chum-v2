package engine

import (
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
)

// ReviewWorkflow resumes the review/merge pipeline for an orphaned PR.
//
// When AgentWorkflow dies after creating a PR but before the review loop
// completes, the task lands in "needs_review" with a PR number but no
// running workflow. ReviewWorkflow picks up from that point:
//
//	GetPRInfo → Review Loop → Merge → Close
//
// No setup/execute/dod — assumes the PR already exists and passed DoD.
func ReviewWorkflow(ctx workflow.Context, req ReviewRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("ReviewWorkflow started", "TaskID", req.TaskID, "PR", req.PRNumber)

	var a *Activities
	reviewRoundsVersion := workflow.GetVersion(ctx, "review-configurable-review-rounds", workflow.DefaultVersion, 1)

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
		StartToCloseTimeout: reviewTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	// Mark task as running so the dispatcher doesn't double-pick it.
	markCtx := workflow.WithActivityOptions(ctx, shortOpts)
	_ = workflow.ExecuteActivity(markCtx, a.CloseTaskActivity, req.TaskID, "running").Get(ctx, nil)

	// === GET PR INFO ===
	prCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var prInfo PRInfo
	if err := workflow.ExecuteActivity(prCtx, a.GetPRInfoActivity, req.WorkDir, req.PRNumber).Get(ctx, &prInfo); err != nil {
		logger.Error("Failed to get PR info", "error", err)
		return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "pr_info_failed",
			PRNumber:  req.PRNumber,
		})
	}

	// === RESOLVE REVIEWER ===
	var reviewerLogin string
	if err := workflow.ExecuteActivity(prCtx, a.ResolveReviewerLoginActivity, req.WorkDir).Get(ctx, &reviewerLogin); err != nil {
		logger.Error("Reviewer login resolution failed", "error", err)
		return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "reviewer_error",
			PRNumber:  prInfo.Number,
			ReviewURL: prInfo.URL,
		})
	}

	// === REVIEW LOOP (identical to AgentWorkflow) ===
	maxReviewRounds := 2
	if reviewRoundsVersion == 1 && req.MaxReviewRounds > 0 {
		maxReviewRounds = req.MaxReviewRounds
	}
	reviewCtx := workflow.WithActivityOptions(ctx, reviewOpts)

	for round := 1; round <= maxReviewRounds; round++ {
		logger.Info("Review round", "Round", round, "PR", prInfo.Number)

		var draft ReviewDraft
		if err := workflow.ExecuteActivity(reviewCtx, a.RunReviewActivity,
			req.WorkDir, prInfo.Number, round, req.Agent).Get(ctx, &draft); err != nil {
			logger.Error("Reviewer run failed", "error", err)
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
				PRNumber:  prInfo.Number,
				ReviewURL: prInfo.URL,
			})
		}

		if err := workflow.ExecuteActivity(prCtx, a.GuardReviewerCleanActivity, req.WorkDir).Get(ctx, nil); err != nil {
			logger.Error("Reviewer guard failed", "error", err)
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_modified_code",
				PRNumber:  prInfo.Number,
				ReviewURL: prInfo.URL,
			})
		}

		var submitted ReviewResult
		if err := workflow.ExecuteActivity(prCtx, a.SubmitReviewActivity,
			req.WorkDir, prInfo.Number, round, reviewerLogin, prInfo.HeadSHA, draft.Signal, draft.Body).Get(ctx, &submitted); err != nil {
			logger.Error("Submit review failed", "error", err)
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "review_submit_failed",
				PRNumber:  prInfo.Number,
				ReviewURL: prInfo.URL,
			})
		}

		var state ReviewResult
		if err := workflow.ExecuteActivity(prCtx, a.CheckPRStateActivity,
			req.WorkDir, prInfo.Number, round, reviewerLogin, prInfo.HeadSHA).Get(ctx, &state); err != nil {
			logger.Error("Check review state failed", "error", err)
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
				PRNumber:  prInfo.Number,
				ReviewURL: submitted.ReviewURL,
			})
		}

		switch state.Outcome {
		case ReviewApproved:
			var merge MergeResult
			if err := workflow.ExecuteActivity(prCtx, a.MergePRActivity, req.WorkDir, prInfo.Number).Get(ctx, &merge); err != nil {
				logger.Error("Merge activity failed", "error", err)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "merge_failed",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			if merge.Merged {
				logger.Info("Task merged successfully via ReviewWorkflow", "TaskID", req.TaskID, "PR", prInfo.Number)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseCompleted,
					SubReason: "completed",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			sub := merge.SubReason
			if sub == "" {
				sub = "merge_blocked"
			}
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: sub,
				PRNumber:  prInfo.Number,
				ReviewURL: state.ReviewURL,
			})

		case ReviewChangesRequested:
			if round == maxReviewRounds {
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "max_rounds_reached",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}

			// Re-execute with reviewer feedback.
			feedback := strings.TrimSpace(state.Comments)
			if state.ReviewID > 0 {
				var inline string
				if err := workflow.ExecuteActivity(prCtx, a.ReadReviewFeedbackActivity,
					req.WorkDir, prInfo.Number, state.ReviewID).Get(ctx, &inline); err == nil {
					inline = strings.TrimSpace(inline)
					if inline != "" {
						if feedback != "" {
							feedback += "\n\n"
						}
						feedback += "Inline comments:\n" + inline
					}
				}
			}
			prompt := augmentPromptWithReviewFeedback(req.Prompt, round, feedback)

			taskReq := TaskRequest{
				TaskID:  req.TaskID,
				Project: req.Project,
				Prompt:  prompt,
				WorkDir: req.WorkDir,
				Agent:   req.Agent,
				Model:   req.Model,
			}

			execCtx := workflow.WithActivityOptions(ctx, execOpts)
			var execResult ExecResult
			if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, taskReq).Get(ctx, &execResult); err != nil {
				logger.Error("Re-execute failed after review changes", "error", err)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "exec_failed",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}

			// Re-run DoD
			var dodResult gitpkg.DoDResult
			dodCtx := workflow.WithActivityOptions(ctx, dodOpts)
			if err := workflow.ExecuteActivity(dodCtx, a.DoDCheckActivity, req.WorkDir, req.Project).Get(ctx, &dodResult); err != nil {
				logger.Error("DoD error after review changes", "error", err)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "dod_error",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			if !dodResult.Passed {
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseDoDFailed,
					SubReason: "dod_failed",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}

			// Push updated code
			pushCtx := workflow.WithActivityOptions(ctx, shortOpts)
			if err := workflow.ExecuteActivity(pushCtx, a.PushActivity, req.WorkDir).Get(ctx, nil); err != nil {
				logger.Error("Push failed after review changes", "error", err)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "push_failed",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}

			// Refresh PR head SHA
			var refreshed PRInfo
			if err := workflow.ExecuteActivity(prCtx, a.GetPRInfoActivity, req.WorkDir, prInfo.Number).Get(ctx, &refreshed); err != nil {
				logger.Error("Failed to refresh PR head SHA after push", "error", err)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "reviewer_error",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			if refreshed.HeadSHA == "" {
				logger.Error("Refreshed PR metadata missing head SHA", "PR", prInfo.Number)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "reviewer_error",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			prInfo = refreshed

		case ReviewNoActivity:
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "no_reviewer_activity",
				PRNumber:  prInfo.Number,
				ReviewURL: state.ReviewURL,
			})

		default:
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
				PRNumber:  prInfo.Number,
				ReviewURL: state.ReviewURL,
			})
		}
	}

	return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
		Reason:    CloseNeedsReview,
		SubReason: "max_rounds_reached",
		PRNumber:  prInfo.Number,
		ReviewURL: prInfo.URL,
	})
}

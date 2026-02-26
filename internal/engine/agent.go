package engine

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
)

// AgentWorkflow is the core CHUM execution loop:
//
//	SetupWorktree → Execute → DoD → (pass: Push+PR+Close / fail: Close) → Cleanup
//
// Tasks arrive fully planned and scoped from beads. No planning step —
// CHUM executes, validates, and ships.
func AgentWorkflow(ctx workflow.Context, req TaskRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("AgentWorkflow started", "TaskID", req.TaskID, "Agent", req.Agent)

	var a *Activities

	// --- Activity options ---
	shortOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	execOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	dodOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	reviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	baseWorkDir := req.WorkDir

	// === WORKTREE SETUP (mandatory) ===
	// Agents must never work on master. Every task gets its own worktree
	// on a feature branch. If worktree setup fails, the task fails.
	wtCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var worktreePath string
	if err := workflow.ExecuteActivity(wtCtx, a.SetupWorktreeActivity, baseWorkDir, req.TaskID).Get(ctx, &worktreePath); err != nil {
		logger.Error("Worktree setup failed — refusing to work on master", "error", err)
		_ = closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "worktree_failed",
		})
		return fmt.Errorf("worktree setup failed (will not work on master): %w", err)
	}
	req.WorkDir = worktreePath
	logger.Info("Worktree isolated", "path", worktreePath)

	// cleanup runs on every exit path
	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		cleanCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(cleanCtx, a.CleanupWorktreeActivity, baseWorkDir, worktreePath).Get(ctx, nil)
	}
	defer cleanup()

	// === EXECUTE ===
	execCtx := workflow.WithActivityOptions(ctx, execOpts)
	var execResult ExecResult
	if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, req).Get(ctx, &execResult); err != nil {
		logger.Error("Execute failed", "error", err)
		_ = closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "exec_failed",
		})
		return fmt.Errorf("execute failed: %w", err)
	}
	logger.Info("Execute complete", "ExitCode", execResult.ExitCode)

	// === DOD CHECK ===
	dodCtx := workflow.WithActivityOptions(ctx, dodOpts)
	var dodResult gitpkg.DoDResult
	if err := workflow.ExecuteActivity(dodCtx, a.DoDCheckActivity, req.WorkDir, req.Project).Get(ctx, &dodResult); err != nil {
		logger.Error("DoD check error", "error", err)
		_ = closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "dod_error",
		})
		return fmt.Errorf("DoD error: %w", err)
	}

	if !dodResult.Passed {
		logger.Warn("DoD FAILED — closing task", "Failures", dodResult.Failures)
		_ = closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseDoDFailed,
			SubReason: "dod_failed",
		})
		return fmt.Errorf("DoD failed: %v", dodResult.Failures)
	}

	// === Push + PR ===
	logger.Info("DoD PASSED — pushing and creating PR")

	pushCtx := workflow.WithActivityOptions(ctx, shortOpts)
	if err := workflow.ExecuteActivity(pushCtx, a.PushActivity, req.WorkDir).Get(ctx, nil); err != nil {
		logger.Error("Push failed", "error", err)
		return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "push_failed",
		})
	}

	prTitle := truncateForTitle(req.Prompt, 72)
	prCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var prInfo PRInfo
	if err := workflow.ExecuteActivity(prCtx, a.CreatePRInfoActivity, req.WorkDir, prTitle).Get(ctx, &prInfo); err != nil {
		logger.Error("PR creation failed", "error", err)
		return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "pr_create_failed",
		})
	}

	// Resolve reviewer GitHub identity once.
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

	const maxReviewRounds = 2
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
				logger.Info("Task merged successfully", "TaskID", req.TaskID, "PR", prInfo.Number)
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
			req.Prompt = augmentPromptWithReviewFeedback(req.Prompt, round, feedback)

			if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, req).Get(ctx, &execResult); err != nil {
				logger.Error("Re-execute failed after review changes", "error", err)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "exec_failed",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
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
			if err := workflow.ExecuteActivity(pushCtx, a.PushActivity, req.WorkDir).Get(ctx, nil); err != nil {
				logger.Error("Push failed after review changes", "error", err)
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "push_failed",
					PRNumber:  prInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}

			var refreshed PRInfo
			if err := workflow.ExecuteActivity(prCtx, a.GetPRInfoActivity, req.WorkDir, prInfo.Number).Get(ctx, &refreshed); err == nil && refreshed.HeadSHA != "" {
				prInfo = refreshed
			}

		case ReviewNoActivity:
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "no_reviewer_activity",
				PRNumber:  prInfo.Number,
				ReviewURL: state.ReviewURL,
			})

		case ReviewerFailed:
			return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
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

// truncateForTitle extracts a short title from a task prompt for PR titles.
func truncateForTitle(prompt string, maxLen int) string {
	// Use first line as title
	if idx := len(prompt); idx > 0 {
		lines := prompt
		if nlIdx := indexOf(lines, '\n'); nlIdx >= 0 {
			lines = lines[:nlIdx]
		}
		if len(lines) > maxLen {
			return lines[:maxLen]
		}
		return lines
	}
	return "chum: automated change"
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func augmentPromptWithReviewFeedback(prompt string, round int, feedback string) string {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		feedback = "No detailed feedback was provided; address review concerns conservatively."
	}
	return fmt.Sprintf("%s\n\nReviewer feedback (round %d):\n%s", prompt, round, feedback)
}

func closeAndNotify(ctx workflow.Context, opts workflow.ActivityOptions, taskID string, detail CloseDetail) error {
	var a *Activities
	actCtx := workflow.WithActivityOptions(ctx, opts)
	_ = workflow.ExecuteActivity(actCtx, a.CloseTaskWithDetailActivity, taskID, detail).Get(ctx, nil)

	message := fmt.Sprintf("task=%s reason=%s sub_reason=%s pr=%d review=%s",
		taskID, detail.Reason, detail.SubReason, detail.PRNumber, detail.ReviewURL)
	_ = workflow.ExecuteActivity(actCtx, a.NotifyActivity, message).Get(ctx, nil)
	return nil
}

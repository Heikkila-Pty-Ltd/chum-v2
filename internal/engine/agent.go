package engine

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
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

	// --- Activity options (from config via dispatcher, with defaults) ---
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
	dodOpts := workflow.ActivityOptions{
		StartToCloseTimeout: reviewTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	reviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: reviewTimeout,
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
		if cerr := closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "worktree_failed",
		}); cerr != nil {
			return fmt.Errorf("worktree setup failed: %w (close/notify failed: %v)", err, cerr)
		}
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

	// === DECOMPOSE ===
	// Version gate: workflows started before decomposition was added must skip
	// this block to avoid Temporal nondeterminism errors during replay.
	decompVersion := workflow.GetVersion(ctx, "add-decompose", workflow.DefaultVersion, 1)
	if decompVersion == 1 {
		// Every task must pass through decomposition. Subtasks (already decomposed)
		// skip this step. If decomposition fails, the task fails — no direct execution
		// without decomposition.
		if req.ParentID != "" {
			logger.Info("Subtask — skipping decomposition", "ParentID", req.ParentID)
		} else {
			decompCtx := workflow.WithActivityOptions(ctx, dodOpts)
			var decompResult types.DecompResult
			if err := workflow.ExecuteActivity(decompCtx, a.DecomposeActivity, req).Get(ctx, &decompResult); err != nil {
				logger.Error("Decomposition failed", "error", err)
				if cerr := closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "decompose_failed",
				}); cerr != nil {
					return fmt.Errorf("decompose failed: %w (close/notify failed: %v)", err, cerr)
				}
				return fmt.Errorf("decompose failed: %w", err)
			}
			if !decompResult.Atomic && len(decompResult.Steps) > 0 {
				var subtaskIDs []string
				if err := workflow.ExecuteActivity(decompCtx, a.CreateSubtasksActivity,
					req.TaskID, req.Project, decompResult.Steps).Get(ctx, &subtaskIDs); err != nil {
					logger.Error("Failed to create subtasks", "error", err)
					if cerr := closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
						Reason:    CloseNeedsReview,
						SubReason: "subtask_creation_failed",
					}); cerr != nil {
						return fmt.Errorf("subtask creation failed: %w (close/notify failed: %v)", err, cerr)
					}
					return fmt.Errorf("subtask creation failed: %w", err)
				}
				logger.Info("Task decomposed", "ParentID", req.TaskID, "Subtasks", len(subtaskIDs))
				sendNotification(ctx, shortOpts, NotifyRequest{
					Event:  "decomposed",
					TaskID: req.TaskID,
					Extra:  map[string]string{"subtasks": fmt.Sprintf("%d", len(subtaskIDs))},
				})
				cleanup()
				return closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
					Reason:    CloseDecomposed,
					SubReason: "decomposed",
				})
			}
		}
	}

	// === EXECUTE ===
	sendNotification(ctx, shortOpts, NotifyRequest{
		Event:  "execute",
		TaskID: req.TaskID,
		Extra:  map[string]string{"agent": req.Agent},
	})
	execCtx := workflow.WithActivityOptions(ctx, execOpts)
	var execResult ExecResult
	if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, req).Get(ctx, &execResult); err != nil {
		logger.Error("Execute failed", "error", err)
		if cerr := closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "exec_failed",
		}); cerr != nil {
			return fmt.Errorf("execute failed: %w (close/notify failed: %v)", err, cerr)
		}
		return fmt.Errorf("execute failed: %w", err)
	}
	logger.Info("Execute complete", "ExitCode", execResult.ExitCode)

	// === DOD CHECK ===
	dodCtx := workflow.WithActivityOptions(ctx, dodOpts)
	var dodResult gitpkg.DoDResult
	if err := workflow.ExecuteActivity(dodCtx, a.DoDCheckActivity, req.WorkDir, req.Project).Get(ctx, &dodResult); err != nil {
		logger.Error("DoD check error", "error", err)
		if cerr := closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "dod_error",
		}); cerr != nil {
			return fmt.Errorf("DoD error: %w (close/notify failed: %v)", err, cerr)
		}
		return fmt.Errorf("DoD error: %w", err)
	}

	if !dodResult.Passed {
		logger.Warn("DoD FAILED — closing task", "Failures", dodResult.Failures)
		sendNotification(ctx, shortOpts, NotifyRequest{
			Event:  "dod_fail",
			TaskID: req.TaskID,
			Extra:  map[string]string{"failures": fmt.Sprintf("%v", dodResult.Failures)},
		})
		if cerr := closeAndNotify(ctx, shortOpts, req.TaskID, CloseDetail{
			Reason:    CloseDoDFailed,
			SubReason: "dod_failed",
		}); cerr != nil {
			return fmt.Errorf("DoD failed: %v (close/notify failed: %v)", dodResult.Failures, cerr)
		}
		return fmt.Errorf("DoD failed: %v", dodResult.Failures)
	}

	// === Push + PR ===
	logger.Info("DoD PASSED — pushing and creating PR")
	sendNotification(ctx, shortOpts, NotifyRequest{
		Event:  "dod_pass",
		TaskID: req.TaskID,
	})

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
	sendNotification(ctx, shortOpts, NotifyRequest{
		Event:  "pr_created",
		TaskID: req.TaskID,
		Extra:  map[string]string{"pr": fmt.Sprintf("%d", prInfo.Number), "url": prInfo.URL},
	})

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
			sendNotification(ctx, shortOpts, NotifyRequest{
				Event:  "review_approved",
				TaskID: req.TaskID,
				Extra:  map[string]string{"reviewer": reviewerLogin},
			})
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
				sendNotification(ctx, shortOpts, NotifyRequest{
					Event:  "merged",
					TaskID: req.TaskID,
					Extra:  map[string]string{"pr": fmt.Sprintf("%d", prInfo.Number)},
				})
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
			sendNotification(ctx, shortOpts, NotifyRequest{
				Event:  "review_changes",
				TaskID: req.TaskID,
				Extra:  map[string]string{"reviewer": reviewerLogin, "round": fmt.Sprintf("%d", round)},
			})
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
		if nlIdx := strings.IndexByte(lines, '\n'); nlIdx >= 0 {
			lines = lines[:nlIdx]
		}
		if len(lines) > maxLen {
			return lines[:maxLen]
		}
		return lines
	}
	return "chum: automated change"
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
	closeErr := workflow.ExecuteActivity(actCtx, a.CloseTaskWithDetailActivity, taskID, detail).Get(ctx, nil)

	// Map CloseDetail → structured NotifyRequest for themed output.
	event := "escalate"
	switch detail.Reason {
	case CloseCompleted:
		event = "complete"
	case CloseDoDFailed:
		event = "dod_fail"
	case CloseDecomposed:
		event = "decomposed"
	}
	req := NotifyRequest{
		Event:  event,
		TaskID: taskID,
		Extra: map[string]string{
			"reason":     string(detail.Reason),
			"sub_reason": detail.SubReason,
			"pr":         fmt.Sprintf("%d", detail.PRNumber),
			"review_url": detail.ReviewURL,
		},
	}
	// Fire-and-forget — notification failures must never block close.
	_ = workflow.ExecuteActivity(actCtx, a.NotifyActivity, req).Get(ctx, nil)

	if closeErr != nil {
		return fmt.Errorf("close task failed: %w", closeErr)
	}
	return nil
}

// sendNotification sends a fire-and-forget themed notification via NotifyActivity.
// Errors are swallowed — notifications must never block workflow progress.
func sendNotification(ctx workflow.Context, opts workflow.ActivityOptions, req NotifyRequest) {
	var a *Activities
	nCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         opts.RetryPolicy,
	})
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, req).Get(ctx, nil)
}

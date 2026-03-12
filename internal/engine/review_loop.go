package engine

import (
	"fmt"
	"strings"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"go.temporal.io/sdk/workflow"
)

// reviewLoopParams configures the shared review loop used by both
// AgentWorkflow and ReviewWorkflow.
type reviewLoopParams struct {
	TaskID          string
	Project         string
	Prompt          string
	WorkDir         string
	Agent           string
	Model           string
	MaxReviewRounds int
	PRInfo          PRInfo
	ReviewerLogin   string

	ShortOpts  workflow.ActivityOptions
	ExecOpts   workflow.ActivityOptions
	ReviewOpts workflow.ActivityOptions
	DoDOpts    workflow.ActivityOptions

	// CloseFn is the callback for closing a task with a detail record.
	// AgentWorkflow passes closeAndTrace (with trace recording);
	// ReviewWorkflow passes closeAndNotify.
	CloseFn func(CloseDetail) error

	// OnTokens is called after each execution with token/cost data.
	// May be nil (ReviewWorkflow doesn't track tokens).
	OnTokens func(input, output int, cost float64)

	// ClassifyDoD controls whether DoD failures after re-execution
	// are classified (AgentWorkflow does; ReviewWorkflow does not).
	ClassifyDoD bool
}

// reviewLoop runs the review/re-execute/merge loop shared between
// AgentWorkflow and ReviewWorkflow. It must produce the same Temporal
// activity call sequence as the original inline loops to maintain
// replay safety for in-flight workflows.
func reviewLoop(ctx workflow.Context, p *reviewLoopParams) error {
	logger := workflow.GetLogger(ctx)
	var a *Activities

	reviewCtx := workflow.WithActivityOptions(ctx, p.ReviewOpts)
	prCtx := workflow.WithActivityOptions(ctx, p.ShortOpts)

	for round := 1; round <= p.MaxReviewRounds; round++ {
		logger.Info("Review round", "Round", round, "PR", p.PRInfo.Number)

		var draft ReviewDraft
		if err := workflow.ExecuteActivity(reviewCtx, a.RunReviewActivity,
			p.WorkDir, p.PRInfo.Number, round, p.Agent).Get(ctx, &draft); err != nil {
			logger.Error("Reviewer run failed", "error", err)
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
				PRNumber:  p.PRInfo.Number,
				ReviewURL: p.PRInfo.URL,
			})
		}

		if err := workflow.ExecuteActivity(prCtx, a.GuardReviewerCleanActivity, p.WorkDir).Get(ctx, nil); err != nil {
			logger.Error("Reviewer guard failed", "error", err)
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_modified_code",
				PRNumber:  p.PRInfo.Number,
				ReviewURL: p.PRInfo.URL,
			})
		}

		var submitted ReviewResult
		if err := workflow.ExecuteActivity(prCtx, a.SubmitReviewActivity,
			p.WorkDir, p.PRInfo.Number, round, p.ReviewerLogin, p.PRInfo.HeadSHA, draft.Signal, draft.Body).Get(ctx, &submitted); err != nil {
			logger.Error("Submit review failed", "error", err)
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "review_submit_failed",
				PRNumber:  p.PRInfo.Number,
				ReviewURL: p.PRInfo.URL,
			})
		}

		var state ReviewResult
		if err := workflow.ExecuteActivity(prCtx, a.CheckPRStateActivity,
			p.WorkDir, p.PRInfo.Number, round, p.ReviewerLogin, p.PRInfo.HeadSHA).Get(ctx, &state); err != nil {
			logger.Error("Check review state failed", "error", err)
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
				PRNumber:  p.PRInfo.Number,
				ReviewURL: submitted.ReviewURL,
			})
		}

		switch state.Outcome {
		case ReviewApproved:
			var merge MergeResult
			if err := workflow.ExecuteActivity(prCtx, a.MergePRActivity, p.WorkDir, p.PRInfo.Number).Get(ctx, &merge); err != nil {
				logger.Error("Merge activity failed", "error", err)
				return p.CloseFn(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "merge_failed",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			if merge.Merged {
				logger.Info("Task merged successfully", "TaskID", p.TaskID, "PR", p.PRInfo.Number)
				return p.CloseFn(CloseDetail{
					Reason:    CloseCompleted,
					SubReason: "completed",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			sub := merge.SubReason
			if sub == "" {
				sub = "merge_blocked"
			}
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: sub,
				PRNumber:  p.PRInfo.Number,
				ReviewURL: state.ReviewURL,
			})

		case ReviewChangesRequested:
			if round == p.MaxReviewRounds {
				return p.CloseFn(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "max_rounds_reached",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}

			feedback := strings.TrimSpace(state.Comments)
			if state.ReviewID > 0 {
				var inline string
				if err := workflow.ExecuteActivity(prCtx, a.ReadReviewFeedbackActivity,
					p.WorkDir, p.PRInfo.Number, state.ReviewID).Get(ctx, &inline); err == nil {
					inline = strings.TrimSpace(inline)
					if inline != "" {
						if feedback != "" {
							feedback += "\n\n"
						}
						feedback += "Inline comments:\n" + inline
					}
				}
			}
			p.Prompt = augmentPromptWithReviewFeedback(p.Prompt, round, feedback)

			execCtx := workflow.WithActivityOptions(ctx, p.ExecOpts)
			taskReq := TaskRequest{
				TaskID:  p.TaskID,
				Project: p.Project,
				Prompt:  p.Prompt,
				WorkDir: p.WorkDir,
				Agent:   p.Agent,
				Model:   p.Model,
			}
			taskReqAfterExec, reExecResult, err := executeWithProviderFallback(execCtx, taskReq)
			if err != nil {
				logger.Error("Re-execute failed after review changes", "error", err)
				return p.CloseFn(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "exec_failed",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			p.Agent = taskReqAfterExec.Agent
			p.Model = taskReqAfterExec.Model
			if p.OnTokens != nil {
				p.OnTokens(reExecResult.InputTokens, reExecResult.OutputTokens, reExecResult.CostUSD)
			}

			dodCtx := workflow.WithActivityOptions(ctx, p.DoDOpts)
			var dodResult gitpkg.DoDResult
			if err := workflow.ExecuteActivity(dodCtx, a.DoDCheckActivity, p.WorkDir, p.Project).Get(ctx, &dodResult); err != nil {
				logger.Error("DoD error after review changes", "error", err)
				return p.CloseFn(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "dod_error",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			if !dodResult.Passed {
				detail := CloseDetail{
					Reason:    CloseDoDFailed,
					SubReason: "dod_failed",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				}
				if p.ClassifyDoD {
					failureMsg := BuildClassifierInput(dodResult)
					cat, sum := ClassifyFailure(failureMsg)
					detail.SubReason = string(cat)
					detail.Category = string(cat)
					detail.Summary = sum
					logger.Warn("DoD FAILED after review changes", "Category", cat, "Summary", sum)
				}
				return p.CloseFn(detail)
			}

			pushCtx := workflow.WithActivityOptions(ctx, p.ShortOpts)
			if err := workflow.ExecuteActivity(pushCtx, a.PushActivity, p.WorkDir).Get(ctx, nil); err != nil {
				logger.Error("Push failed after review changes", "error", err)
				return p.CloseFn(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "push_failed",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}

			var refreshed PRInfo
			if err := workflow.ExecuteActivity(prCtx, a.GetPRInfoActivity, p.WorkDir, p.PRInfo.Number).Get(ctx, &refreshed); err != nil {
				logger.Error("Failed to refresh PR head SHA after push", "error", err)
				return p.CloseFn(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "reviewer_error",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			if refreshed.HeadSHA == "" {
				logger.Error("Refreshed PR metadata missing head SHA", "PR", p.PRInfo.Number)
				return p.CloseFn(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "reviewer_error",
					PRNumber:  p.PRInfo.Number,
					ReviewURL: state.ReviewURL,
				})
			}
			p.PRInfo = refreshed

		case ReviewNoActivity:
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "no_reviewer_activity",
				PRNumber:  p.PRInfo.Number,
				ReviewURL: state.ReviewURL,
			})

		case ReviewerFailed:
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
				PRNumber:  p.PRInfo.Number,
				ReviewURL: state.ReviewURL,
			})

		default:
			return p.CloseFn(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "reviewer_error",
				PRNumber:  p.PRInfo.Number,
				ReviewURL: state.ReviewURL,
			})
		}
	}

	return p.CloseFn(CloseDetail{
		Reason:    CloseNeedsReview,
		SubReason: "max_rounds_reached",
		PRNumber:  p.PRInfo.Number,
		ReviewURL: p.PRInfo.URL,
	})
}

// sanitizePromptInput strips known prompt injection patterns from
// user-controlled content before it enters agent prompts. This is a
// defense-in-depth measure — not a replacement for proper sandboxing.
func sanitizePromptInput(s string) string {
	// Strip common injection delimiters and override patterns.
	replacer := strings.NewReplacer(
		"<|system|>", "",
		"<|user|>", "",
		"<|assistant|>", "",
		"<|im_start|>", "",
		"<|im_end|>", "",
		"<|endoftext|>", "",
	)
	s = replacer.Replace(s)

	// Strip lines that attempt to override instructions.
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		upper := strings.ToUpper(strings.TrimSpace(line))
		if strings.HasPrefix(upper, "IGNORE PREVIOUS") ||
			strings.HasPrefix(upper, "IGNORE ALL PREVIOUS") ||
			strings.HasPrefix(upper, "DISREGARD PREVIOUS") ||
			strings.HasPrefix(upper, "FORGET PREVIOUS") ||
			strings.HasPrefix(upper, "NEW INSTRUCTIONS:") ||
			strings.HasPrefix(upper, "SYSTEM:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// wrapUserContent wraps user-controlled content with clear boundary
// markers so the LLM can distinguish instructions from data.
func wrapUserContent(label, content string) string {
	return fmt.Sprintf("--- BEGIN %s ---\n%s\n--- END %s ---", label, sanitizePromptInput(content), label)
}

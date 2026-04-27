package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/workflow"

	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
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

	// Version gate: trace recording added after initial release.
	traceVersion := workflow.GetVersion(ctx, "add-trace-recording", workflow.DefaultVersion, 1)
	reviewRoundsVersion := workflow.GetVersion(ctx, "agent-configurable-review-rounds", workflow.DefaultVersion, 1)
	startTime := workflow.Now(ctx)

	// --- Activity options (from config via dispatcher, with defaults) ---
	opts := BuildActivityOpts(req.ShortTimeout, req.ExecTimeout, req.ReviewTimeout)
	shortOpts := opts.Short
	execOpts := opts.Exec
	dodOpts := opts.DoD
	reviewOpts := opts.Review

	baseWorkDir := req.WorkDir

	// === WORKTREE SETUP (mandatory) ===
	// Agents must never work on master. Every task gets its own worktree
	// on a feature branch. If worktree setup fails, the task fails.

	// cleanup runs on every exit path, even if setup fails.
	// Use SideEffect to record os.TempDir() deterministically — replaying on
	// a different worker (or after TMPDIR change) must produce the same path.
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

	// Token/cost accumulators — summed across all execution attempts
	// (initial exec + re-executions after review changes_requested).
	var totalInputTokens, totalOutputTokens int
	var totalCostUSD float64

	// closeAndTrace wraps closeAndNotify and records an execution trace.
	closeAndTrace := func(detail CloseDetail) error {
		cerr := closeAndNotify(ctx, shortOpts, req.TaskID, detail, req.Metadata)
		if traceVersion == 1 {
			traceCtx := workflow.WithActivityOptions(ctx, shortOpts)
			info := workflow.GetInfo(ctx)
			_ = workflow.ExecuteActivity(traceCtx, a.RecordTraceActivity, TraceOutcome{
				TaskID:       req.TaskID,
				SessionID:    info.WorkflowExecution.RunID,
				Agent:        req.Agent,
				Model:        req.Model,
				Tier:         req.Tier,
				Reason:       string(detail.Reason),
				SubReason:    detail.SubReason,
				Duration:     workflow.Now(ctx).Sub(startTime),
				InputTokens:  totalInputTokens,
				OutputTokens: totalOutputTokens,
				CostUSD:      totalCostUSD,
			}).Get(ctx, nil)
		}
		return cerr
	}
	wtCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var worktreePath string
	if err := workflow.ExecuteActivity(wtCtx, a.SetupWorktreeActivity, baseWorkDir, req.TaskID).Get(ctx, &worktreePath); err != nil {
		logger.Error("Worktree setup failed — refusing to work on master", "error", err)
		if cerr := closeAndTrace(CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "worktree_failed",
		}); cerr != nil {
			return fmt.Errorf("worktree setup failed: %w (close/notify failed: %v)", err, cerr)
		}
		return fmt.Errorf("worktree setup failed (will not work on master): %w", err)
	}
	req.WorkDir = worktreePath
	logger.Info("Worktree isolated", "path", worktreePath)

	// === WORKTREE SETUP (project-specific) ===
	// Version gate: setup commands added after initial release.
	setupVersion := workflow.GetVersion(ctx, "add-setup-commands", workflow.DefaultVersion, 1)
	if setupVersion == 1 {
		setupCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(setupCtx, a.RunSetupCommandsActivity, worktreePath, req.Project).Get(ctx, nil)
	}

	// === RESOLVE EXECUTION MODE (needed before decompose decision) ===
	modeVersion := workflow.GetVersion(ctx, "add-execution-modes", workflow.DefaultVersion, 1)
	execMode := req.ExecutionMode
	if execMode == "" {
		execMode = "code_change"
	}

	// === DECOMPOSE ===
	// Version gate: workflows started before decomposition was added must skip
	// this block to avoid Temporal nondeterminism errors during replay.
	// Skip decomposition for research and command modes — they are atomic by nature.
	decompVersion := workflow.GetVersion(ctx, "add-decompose", workflow.DefaultVersion, 1)
	if decompVersion == 1 && execMode == "code_change" {
		// Code change tasks pass through decomposition. Subtasks (already decomposed)
		// skip this step. If decomposition fails, the task fails — no direct execution
		// without decomposition.
		if req.ParentID != "" {
			logger.Info("Subtask — skipping decomposition", "ParentID", req.ParentID)
		} else {
			decompCtx := workflow.WithActivityOptions(ctx, dodOpts)
			var decompResult types.DecompResult
			if err := workflow.ExecuteActivity(decompCtx, a.DecomposeActivity, req).Get(ctx, &decompResult); err != nil {
				logger.Error("Decomposition failed", "error", err)
				if cerr := closeAndTrace(CloseDetail{
					Reason:    CloseNeedsReview,
					SubReason: "decompose_failed",
				}); cerr != nil {
					return fmt.Errorf("decompose failed: %w (close/notify failed: %v)", err, cerr)
				}
				return fmt.Errorf("decompose failed: %w", err)
			}
			if !decompResult.Atomic && len(decompResult.Steps) > 0 {
				// Re-decompose any steps exceeding the 15-min threshold.
				steps, flattenErr := flattenDecomposedSteps(ctx, a, req, decompResult.Steps, 2, dodOpts)
				if flattenErr != nil {
					logger.Warn("Re-decomposition failed, using original steps", "error", flattenErr)
					steps = decompResult.Steps
				}
				var subtaskIDs []string
				if err := workflow.ExecuteActivity(decompCtx, a.CreateSubtasksActivity,
					req.TaskID, req.Project, steps).Get(ctx, &subtaskIDs); err != nil {
					logger.Error("Failed to create subtasks", "error", err)
					if cerr := closeAndTrace(CloseDetail{
						Reason:    CloseNeedsReview,
						SubReason: "subtask_creation_failed",
					}); cerr != nil {
						return fmt.Errorf("subtask creation failed: %w (close/notify failed: %v)", err, cerr)
					}
					return fmt.Errorf("subtask creation failed: %w", err)
				}
				logger.Info("Task decomposed", "ParentID", req.TaskID, "Subtasks", len(subtaskIDs))
				cleanup()
				return closeAndTrace(CloseDetail{
					Reason:    CloseDecomposed,
					SubReason: "decomposed",
				})
			}
		}
	}

	// === EXECUTION MODE BRANCHING ===
	if modeVersion == 1 && execMode == "command" {
		// Command mode: run shell commands directly, no LLM, no worktree pipeline.
		commands := strings.Split(req.Metadata["commands"], "\n")
		if len(commands) == 0 || (len(commands) == 1 && commands[0] == "") {
			return closeAndTrace(CloseDetail{
				Reason:    CloseFailed,
				SubReason: "no_commands",
				Summary:   "command mode requires commands in task metadata",
			})
		}
		commandCtx := workflow.WithActivityOptions(ctx, execOpts)
		var output string
		if err := workflow.ExecuteActivity(commandCtx, a.RunCommandActivity, req.WorkDir, commands).Get(ctx, &output); err != nil {
			return closeAndTrace(CloseDetail{
				Reason:    CloseFailed,
				SubReason: "command_failed",
				Summary:   types.Truncate(output, 4000),
			})
		}
		return closeAndTrace(CloseDetail{
			Reason:    CloseCompleted,
			SubReason: "command_output",
			Summary:   types.Truncate(output, 4000),
		})
	}

	if modeVersion == 1 && execMode == "research" {
		// Research mode: run LLM in Plan mode (read-only), skip DoD/Push/PR pipeline.
		execCtx := workflow.WithActivityOptions(ctx, execOpts)
		var execResult ExecResult
		if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, req).Get(ctx, &execResult); err != nil {
			return closeAndTrace(CloseDetail{
				Reason:    CloseNeedsReview,
				SubReason: "research_failed",
			})
		}
		totalInputTokens += execResult.InputTokens
		totalOutputTokens += execResult.OutputTokens
		totalCostUSD += execResult.CostUSD
		return closeAndTrace(CloseDetail{
			Reason:    CloseCompleted,
			SubReason: "research_complete",
			Summary:   types.Truncate(execResult.Output, 4000),
		})
	}

	// === CODE CHANGE: EXECUTE + EXPERIMENT MODE ===
	// When MaxExperimentAttempts > 0, DoD failures trigger a retry loop:
	// revert worktree, augment prompt with failure context, re-execute.
	// Each failure is recorded as a lesson for future tasks.
	experimentMode := req.MaxExperimentAttempts > 0
	maxAttempts := req.MaxExperimentAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1 // default: single attempt, no retry
	}

	var experimentLog []string
	var dodPassed bool
	var dodResult gitpkg.DoDResult
	var experimentAttempt int

	for experimentAttempt = 1; experimentAttempt <= maxAttempts; experimentAttempt++ {
		if experimentAttempt > 1 {
			logger.Info("Experiment retry", "Attempt", experimentAttempt, "Max", maxAttempts)
		}

		// Execute
		execCtx := workflow.WithActivityOptions(ctx, execOpts)
		reqAfterExec, execResult, err := executeWithProviderFallback(execCtx, req)
		if err != nil {
			logger.Error("Execute failed", "error", err)
			if experimentAttempt < maxAttempts {
				failMsg := fmt.Sprintf("Execute failed: %v", err)
				experimentLog = append(experimentLog, failMsg)
				if experimentMode {
					storeExperimentLesson(ctx, shortOpts, a, req, "exec_failure", failMsg, "", nil)
				}
				// Reset worktree for retry
				if resetErr := workflow.ExecuteActivity(
					workflow.WithActivityOptions(ctx, shortOpts),
					a.ResetWorktreeActivity, req.WorkDir,
				).Get(ctx, nil); resetErr != nil {
					logger.Error("Worktree reset failed", "error", resetErr)
				}
				req.Prompt = augmentPromptWithExperimentFailure(req.Prompt, experimentAttempt, failMsg, nil)
				continue
			}
			if cerr := closeAndTrace(CloseDetail{
				Reason:             CloseNeedsReview,
				SubReason:          "exec_failed",
				ExperimentAttempts: experimentAttempt,
				ExperimentLog:      experimentLog,
			}); cerr != nil {
				return fmt.Errorf("execute failed: %w (close/notify failed: %v)", err, cerr)
			}
			return fmt.Errorf("execute failed: %w", err)
		}
		req = reqAfterExec
		totalInputTokens += execResult.InputTokens
		totalOutputTokens += execResult.OutputTokens
		totalCostUSD += execResult.CostUSD
		logger.Info("Execute complete", "ExitCode", execResult.ExitCode, "Attempt", experimentAttempt)

		// DoD check
		dodCtx := workflow.WithActivityOptions(ctx, dodOpts)
		if err := workflow.ExecuteActivity(dodCtx, a.DoDCheckActivity, req.WorkDir, req.Project).Get(ctx, &dodResult); err != nil {
			logger.Error("DoD check error", "error", err)
			if experimentAttempt < maxAttempts {
				failMsg := fmt.Sprintf("DoD check error: %v", err)
				experimentLog = append(experimentLog, failMsg)
				continue
			}
			if cerr := closeAndTrace(CloseDetail{
				Reason:             CloseNeedsReview,
				SubReason:          "dod_error",
				ExperimentAttempts: experimentAttempt,
				ExperimentLog:      experimentLog,
			}); cerr != nil {
				return fmt.Errorf("DoD error: %w (close/notify failed: %v)", err, cerr)
			}
			return fmt.Errorf("DoD error: %w", err)
		}

		if dodResult.Passed {
			dodPassed = true
			break
		}

		// DoD failed
		failureMsg := BuildClassifierInput(dodResult)
		category, summary := ClassifyFailure(failureMsg)
		logger.Warn("DoD FAILED", "Category", category, "Summary", summary, "Attempt", experimentAttempt, "Failures", dodResult.Failures)

		experimentLog = append(experimentLog, fmt.Sprintf("Attempt %d [%s]: %s", experimentAttempt, category, summary))

		// Store lesson for future tasks (experiment mode only)
		if experimentMode {
			detail := fmt.Sprintf("DoD failures on attempt %d: %s", experimentAttempt, failureMsg)
			storeExperimentLesson(ctx, shortOpts, a, req, string(category), summary, detail, nil)
		}

		if experimentAttempt < maxAttempts {
			// Reset worktree and retry with augmented prompt
			if resetErr := workflow.ExecuteActivity(
				workflow.WithActivityOptions(ctx, shortOpts),
				a.ResetWorktreeActivity, req.WorkDir,
			).Get(ctx, nil); resetErr != nil {
				logger.Error("Worktree reset failed", "error", resetErr)
			}
			req.Prompt = augmentPromptWithDoDFailure(req.Prompt, experimentAttempt, dodResult)
			continue
		}

		// Final attempt failed
		if cerr := closeAndTrace(CloseDetail{
			Reason:             CloseDoDFailed,
			SubReason:          string(category),
			Category:           string(category),
			Summary:            summary,
			ExperimentAttempts: experimentAttempt,
			ExperimentLog:      experimentLog,
		}); cerr != nil {
			return fmt.Errorf("DoD failed (%s): %v (close/notify failed: %v)", category, dodResult.Failures, cerr)
		}
		return fmt.Errorf("DoD failed (%s): %v", category, dodResult.Failures)
	}

	if !dodPassed {
		// Should not reach here, but guard against it
		return closeAndTrace(CloseDetail{
			Reason:             CloseDoDFailed,
			SubReason:          "unknown",
			ExperimentAttempts: experimentAttempt,
			ExperimentLog:      experimentLog,
		})
	}

	// === Push + PR ===
	logger.Info("DoD PASSED — pushing and creating PR")

	pushCtx := workflow.WithActivityOptions(ctx, shortOpts)
	if err := workflow.ExecuteActivity(pushCtx, a.PushActivity, req.WorkDir).Get(ctx, nil); err != nil {
		logger.Error("Push failed", "error", err)
		return closeAndTrace(CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "push_failed",
		})
	}

	prTitle := truncateForTitle(req.Prompt, 72)
	prCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var prInfo PRInfo
	if err := workflow.ExecuteActivity(prCtx, a.CreatePRInfoActivity, req.WorkDir, prTitle).Get(ctx, &prInfo); err != nil {
		if isNoCommitsPRCreateError(err) {
			logger.Info("PR creation skipped: branch has no commits ahead of base", "TaskID", req.TaskID)
			return closeAndTrace(CloseDetail{
				Reason:    CloseCompleted,
				SubReason: "no_changes",
			})
		}
		logger.Error("PR creation failed", "error", err)
		return closeAndTrace(CloseDetail{
			Reason:    CloseNeedsReview,
			SubReason: "pr_create_failed",
		})
	}

	// Resolve reviewer GitHub identity once.
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

	// Backward compatibility: workflows that started before this version gate
	// always used 2 rounds. New workflows can consume config-threaded rounds.
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
		OnTokens: func(input, output int, cost float64) {
			totalInputTokens += input
			totalOutputTokens += output
			totalCostUSD += cost
		},
		ClassifyDoD: true,
	})
}

func isNoCommitsPRCreateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no commits between")
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
	return fmt.Sprintf("%s\n\n%s", prompt,
		wrapUserContent(fmt.Sprintf("REVIEWER FEEDBACK ROUND %d", round), feedback))
}

// augmentPromptWithDoDFailure adds DoD failure context to the prompt for experiment retries.
func augmentPromptWithDoDFailure(prompt string, attempt int, dodResult gitpkg.DoDResult) string {
	var failureDetails strings.Builder
	for _, f := range dodResult.Failures {
		failureDetails.WriteString(fmt.Sprintf("- %s\n", f))
	}
	return fmt.Sprintf("%s\n\n%s",
		prompt,
		wrapUserContent(fmt.Sprintf("EXPERIMENT MODE — Attempt %d/%d failed DoD check. Fix the failures below and retry.", attempt, attempt+1),
			failureDetails.String()))
}

// augmentPromptWithExperimentFailure adds a generic experiment failure context to the prompt.
func augmentPromptWithExperimentFailure(prompt string, attempt int, failureMsg string, _ interface{}) string {
	return fmt.Sprintf("%s\n\n%s\nFailure: %s",
		prompt,
		wrapUserContent(fmt.Sprintf("EXPERIMENT MODE — Attempt %d failed. Fix and retry.", attempt), ""),
		failureMsg)
}

// storeExperimentLesson persists a lesson from an experiment failure.
// Returns nil on success or if lesson store is unavailable.
func storeExperimentLesson(ctx workflow.Context, opts workflow.ActivityOptions, a *Activities, req TaskRequest, category, summary, detail string, filePaths []string) {
	if a.Lessons == nil {
		return
	}
	actCtx := workflow.WithActivityOptions(ctx, opts)
	var labels []string
	if category != "" {
		labels = append(labels, "experiment-failure", category)
	}
	_ = workflow.ExecuteActivity(actCtx, a.StoreLessonActivity, LessonParams{
		TaskID:    req.TaskID,
		Project:   req.Project,
		Category:  category,
		Summary:   summary,
		Detail:    detail,
		FilePaths: filePaths,
		Labels:    labels,
	}).Get(ctx, nil)
}

func closeAndNotify(ctx workflow.Context, opts workflow.ActivityOptions, taskID string, detail CloseDetail, metadata ...map[string]string) error {
	var a *Activities
	actCtx := workflow.WithActivityOptions(ctx, opts)
	closeErr := workflow.ExecuteActivity(actCtx, a.CloseTaskWithDetailActivity, taskID, detail).Get(ctx, nil)

	message := fmt.Sprintf("task=%s reason=%s sub_reason=%s pr=%d review=%s",
		taskID, detail.Reason, detail.SubReason, detail.PRNumber, detail.ReviewURL)
	notifyErr := workflow.ExecuteActivity(actCtx, a.NotifyActivity, message).Get(ctx, nil)

	if closeErr != nil {
		return fmt.Errorf("close task failed: %w", closeErr)
	}
	if notifyErr != nil {
		return fmt.Errorf("notify failed: %w", notifyErr)
	}

	// Version-gated: deliver results to callback URL if present in task metadata.
	// Only fires after close+notify succeed — prevents sending callbacks for tasks
	// that CHUM failed to close, which would create inconsistent state with Kaikki.
	callbackVersion := workflow.GetVersion(ctx, "add-callback-activity", workflow.DefaultVersion, 1)
	if callbackVersion >= 1 && len(metadata) > 0 && metadata[0] != nil {
		if callbackURL := metadata[0]["callback_url"]; callbackURL != "" {
			_ = workflow.ExecuteActivity(actCtx, a.CallbackActivity, CallbackInput{
				URL:           callbackURL,
				Token:         metadata[0]["callback_token"],
				ExternalRef:   metadata[0]["external_ref"],
				TaskID:        taskID,
				ExecutionMode: metadata[0]["execution_mode"],
				Detail:        detail,
			}).Get(ctx, nil) // best-effort: don't fail the workflow on callback errors
		}
	}

	return nil
}

func executeWithProviderFallback(ctx workflow.Context, req TaskRequest) (TaskRequest, ExecResult, error) {
	logger := workflow.GetLogger(ctx)
	var a *Activities

	var primary ExecResult
	if err := workflow.ExecuteActivity(ctx, a.ExecuteActivity, req).Get(ctx, &primary); err == nil {
		return req, primary, nil
	} else if !shouldFallbackExecutionError(err) {
		return req, ExecResult{}, err
	} else {
		fallbackReq, ok := withFallbackProvider(req)
		if !ok {
			return req, ExecResult{}, err
		}

		logger.Warn("Primary execute failed; retrying fallback provider",
			"task_id", req.TaskID,
			"from_agent", req.Agent,
			"to_agent", fallbackReq.Agent,
			"error", err.Error(),
		)
		var fallback ExecResult
		if fallbackErr := workflow.ExecuteActivity(ctx, a.ExecuteActivity, fallbackReq).Get(ctx, &fallback); fallbackErr != nil {
			return req, ExecResult{}, fmt.Errorf("primary execute failed: %w; fallback(%s) failed: %v",
				err, fallbackReq.Agent, fallbackErr)
		}
		logger.Info("Fallback execute succeeded",
			"task_id", req.TaskID,
			"agent", fallbackReq.Agent,
		)
		return fallbackReq, fallback, nil
	}
}

func shouldFallbackExecutionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "preflight:") {
		return false
	}
	return strings.Contains(msg, "execute cli:") ||
		strings.Contains(msg, "agent exited with code") ||
		strings.Contains(msg, "rate limited") ||
		strings.Contains(msg, "context cancelled during exec retry")
}

func withFallbackProvider(req TaskRequest) (TaskRequest, bool) {
	nextAgent, nextModel, ok := nextFallbackExecutionProvider(req.Agent)
	if !ok {
		return req, false
	}
	fallbackReq := req
	fallbackReq.Agent = nextAgent
	fallbackReq.Model = strings.TrimSpace(nextModel)
	return fallbackReq, true
}

func nextFallbackExecutionProvider(agent string) (agentCLI string, model string, ok bool) {
	switch llm.NormalizeCLIName(agent) {
	case "gemini":
		return "codex", "", true
	case "codex":
		return "gemini", "", true
	default:
		return "", "", false
	}
}

// maxSingleStepMinutes is the threshold above which a decomposed step
// should be re-decomposed into smaller units.
const maxSingleStepMinutes = 15

// flattenDecomposedSteps recursively re-decomposes any step whose estimate
// exceeds maxSingleStepMinutes. depth limits recursion to prevent infinite loops.
func flattenDecomposedSteps(ctx workflow.Context, a *Activities, req TaskRequest, steps []types.DecompStep, depth int, activityOpts workflow.ActivityOptions) ([]types.DecompStep, error) {
	if depth <= 0 {
		return steps, nil
	}
	var flat []types.DecompStep
	for _, s := range steps {
		if s.Estimate <= maxSingleStepMinutes {
			flat = append(flat, s)
			continue
		}
		subReq := TaskRequest{
			TaskID:  req.TaskID,
			Project: req.Project,
			Prompt:  s.Title + "\n\n" + s.Description,
			WorkDir: req.WorkDir,
			Agent:   req.Agent,
			Model:   req.Model,
		}
		var subResult *types.DecompResult
		decompCtx := workflow.WithActivityOptions(ctx, activityOpts)
		if err := workflow.ExecuteActivity(decompCtx, a.DecomposeActivity, subReq).Get(ctx, &subResult); err != nil {
			s.Estimate = maxSingleStepMinutes
			flat = append(flat, s)
			continue
		}
		if subResult.Atomic || len(subResult.Steps) == 0 {
			s.Estimate = maxSingleStepMinutes
			flat = append(flat, s)
			continue
		}
		subSteps, err := flattenDecomposedSteps(ctx, a, subReq, subResult.Steps, depth-1, activityOpts)
		if err != nil {
			subSteps = subResult.Steps
		}
		flat = append(flat, subSteps...)
	}
	return flat, nil
}

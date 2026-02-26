package engine

import (
	"fmt"
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

	baseWorkDir := req.WorkDir

	// === WORKTREE SETUP (mandatory) ===
	// Agents must never work on master. Every task gets its own worktree
	// on a feature branch. If worktree setup fails, the task fails.
	wtCtx := workflow.WithActivityOptions(ctx, shortOpts)
	var worktreePath string
	if err := workflow.ExecuteActivity(wtCtx, a.SetupWorktreeActivity, baseWorkDir, req.TaskID).Get(ctx, &worktreePath); err != nil {
		logger.Error("Worktree setup failed — refusing to work on master", "error", err)
		closeCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "worktree_failed").Get(ctx, nil)
		return fmt.Errorf("worktree setup failed (will not work on master): %w", err)
	}
	req.WorkDir = worktreePath
	logger.Info("Worktree isolated", "path", worktreePath)

	// cleanup runs on every exit path
	cleanup := func() {
		cleanCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(cleanCtx, a.CleanupWorktreeActivity, baseWorkDir, worktreePath).Get(ctx, nil)
	}

	// === EXECUTE ===
	execCtx := workflow.WithActivityOptions(ctx, execOpts)
	var execResult ExecResult
	if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, req).Get(ctx, &execResult); err != nil {
		logger.Error("Execute failed", "error", err)
		closeCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "exec_failed").Get(ctx, nil)
		cleanup()
		return fmt.Errorf("execute failed: %w", err)
	}
	logger.Info("Execute complete", "ExitCode", execResult.ExitCode)

	// === DOD CHECK ===
	dodCtx := workflow.WithActivityOptions(ctx, dodOpts)
	var dodResult gitpkg.DoDResult
	if err := workflow.ExecuteActivity(dodCtx, a.DoDCheckActivity, req.WorkDir, req.Project).Get(ctx, &dodResult); err != nil {
		logger.Error("DoD check error", "error", err)
		closeCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "dod_error").Get(ctx, nil)
		cleanup()
		return fmt.Errorf("DoD error: %w", err)
	}

	if !dodResult.Passed {
		logger.Warn("DoD FAILED — closing task", "Failures", dodResult.Failures)
		closeCtx := workflow.WithActivityOptions(ctx, shortOpts)
		_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "dod_failed").Get(ctx, nil)
		cleanup()
		return fmt.Errorf("DoD failed: %v", dodResult.Failures)
	}

	// === SUCCESS: Push + PR + Close ===
	logger.Info("DoD PASSED — pushing and creating PR")

	pushCtx := workflow.WithActivityOptions(ctx, shortOpts)
	if err := workflow.ExecuteActivity(pushCtx, a.PushActivity, req.WorkDir).Get(ctx, nil); err != nil {
		logger.Warn("Push failed", "error", err)
	}

	prTitle := truncateForTitle(req.Prompt, 72)
	prCtx := workflow.WithActivityOptions(ctx, shortOpts)
	if err := workflow.ExecuteActivity(prCtx, a.CreatePRActivity, req.WorkDir, prTitle).Get(ctx, nil); err != nil {
		logger.Warn("PR creation failed", "error", err)
	}

	closeCtx := workflow.WithActivityOptions(ctx, shortOpts)
	_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "completed").Get(ctx, nil)

	cleanup()
	logger.Info("AgentWorkflow completed", "TaskID", req.TaskID)
	return nil
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

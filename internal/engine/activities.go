package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/activity"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/notify"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// Activities holds dependencies for Temporal activity methods.
type Activities struct {
	DAG          *dag.DAG
	Config       *config.Config
	Logger       *slog.Logger
	AST          *astpkg.Parser
	BeadsClients map[string]beads.Store
	ChatSend     notify.ChatSender
}

// --- 1. SetupWorktreeActivity ---

// SetupWorktreeActivity creates an isolated git worktree for a task.
func (a *Activities) SetupWorktreeActivity(ctx context.Context, baseDir, taskID string) (string, error) {
	logger := activity.GetLogger(ctx)
	wtDir, err := gitpkg.SetupWorktree(ctx, baseDir, taskID)
	if err != nil {
		return "", fmt.Errorf("setup worktree: %w", err)
	}
	logger.Info("Worktree created", "path", wtDir)
	return wtDir, nil
}

// --- 2. ExecuteActivity ---

// ExecuteActivity runs the LLM CLI to implement a task, then commits changes.
// The task prompt comes directly from beads (description + acceptance criteria).
// AST context is injected so the agent understands the codebase structure.
func (a *Activities) ExecuteActivity(ctx context.Context, req TaskRequest) (*ExecResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Executing task", "TaskID", req.TaskID, "Agent", req.Agent)

	// --- Preflight 1: CLI binary exists ---
	cliName := llm.NormalizeCLIName(req.Agent)
	if _, err := exec.LookPath(cliName); err != nil {
		return nil, fmt.Errorf("PREFLIGHT: CLI %q not found on PATH — cannot execute", cliName)
	}

	// --- Preflight 2: worktree still intact ---
	if _, err := os.Stat(filepath.Join(req.WorkDir, ".git")); err != nil {
		return nil, fmt.Errorf("PREFLIGHT: worktree broken — .git missing in %s", req.WorkDir)
	}

	// --- Preflight 3: not on master/main (worktree enforcement) ---
	if err := gitpkg.AssertFeatureBranch(ctx, req.WorkDir); err != nil {
		return nil, fmt.Errorf("PREFLIGHT: %w", err)
	}

	// --- Preflight 4: project builds before we start ---
	projCfg, ok := a.Config.Projects[req.Project]
	if ok && len(projCfg.DoDChecks) > 0 {
		buildCmd := projCfg.DoDChecks[0]
		parts := strings.Fields(buildCmd)
		if len(parts) > 0 {
			cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
			cmd.Dir = req.WorkDir
			if out, err := cmd.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("PREFLIGHT: project doesn't build before coding — %s failed: %s",
					buildCmd, types.Truncate(string(out), 300))
			}
			logger.Info("Preflight: baseline build OK")
		}
	}

	// Build AST codebase context
	codeContext := a.buildCodebaseContext(ctx, req.WorkDir)

	prompt := fmt.Sprintf(`You are a senior software engineer. Implement the following task.

TASK:
%s

CODEBASE:
%s

Implement this task by modifying the necessary files. Do not explain, just code.`, req.Prompt, codeContext)

	result, err := RunCLIExec(req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("execute CLI: %w", err)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("agent exited with code %d: %s",
			result.ExitCode, types.Truncate(result.Output, 500))
	}

	// Auto-commit any changes the agent made
	commitMsg := fmt.Sprintf("chum: %s\n\nTask: %s", types.Truncate(req.Prompt, 72), req.TaskID)
	committed, err := gitpkg.CommitAll(ctx, req.WorkDir, commitMsg)
	if err != nil {
		logger.Warn("Auto-commit failed", "error", err)
	} else if committed {
		logger.Info("Changes committed")
	} else {
		logger.Warn("Agent produced no file changes")
	}

	return &ExecResult{
		ExitCode: result.ExitCode,
		Output:   result.Output,
	}, nil
}

// --- 3. DoDCheckActivity ---

// DoDCheckActivity runs DoD verification checks (build, test, vet).
func (a *Activities) DoDCheckActivity(ctx context.Context, workDir, project string) (*gitpkg.DoDResult, error) {
	logger := activity.GetLogger(ctx)

	projCfg, ok := a.Config.Projects[project]
	if !ok {
		return nil, fmt.Errorf("project %q not found in config", project)
	}

	checks := projCfg.DoDChecks
	if len(checks) == 0 {
		checks = []string{"go build ./...", "go vet ./..."}
	}

	logger.Info("Running DoD checks", "Checks", len(checks))
	result := gitpkg.RunDoDChecks(ctx, workDir, checks)
	logger.Info("DoD complete", "Passed", result.Passed, "Failures", len(result.Failures))
	return &result, nil
}

// --- 4. PushActivity ---

// PushActivity pushes the feature branch to origin.
func (a *Activities) PushActivity(ctx context.Context, workDir string) error {
	return gitpkg.Push(ctx, workDir)
}

// --- 5. CreatePRActivity ---

// CreatePRActivity creates a pull request for the feature branch.
func (a *Activities) CreatePRActivity(ctx context.Context, workDir, title string) error {
	return gitpkg.CreatePR(ctx, workDir, title)
}

// CreatePRInfoActivity creates a pull request and returns metadata (PR number/head SHA/url).
func (a *Activities) CreatePRInfoActivity(ctx context.Context, workDir, title string) (*PRInfo, error) {
	info, err := gitpkg.CreatePRInfo(ctx, workDir, title)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// GetPRInfoActivity returns metadata for an existing pull request.
func (a *Activities) GetPRInfoActivity(ctx context.Context, workDir string, prNumber int) (*PRInfo, error) {
	info, err := gitpkg.GetPRInfo(ctx, workDir, prNumber)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// --- 6. CloseTaskActivity ---

// CloseTaskActivity sets the task status in the DAG (e.g. "completed", "dod_failed").
func (a *Activities) CloseTaskActivity(ctx context.Context, taskID, status string) error {
	return a.DAG.CloseTask(ctx, taskID, status)
}

// CloseTaskWithDetailActivity updates task status plus structured error_log detail.
// On completion, writes back status to beads (best-effort).
func (a *Activities) CloseTaskWithDetailActivity(ctx context.Context, taskID string, detail CloseDetail) error {
	logger := activity.GetLogger(ctx)
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal close detail: %w", err)
	}
	if err := a.DAG.UpdateTask(ctx, taskID, map[string]any{
		"status":    string(detail.Reason),
		"error_log": string(raw),
	}); err != nil {
		return err
	}

	// Writeback to beads (best-effort, non-fatal).
	// NullStore handles the case where bd is unavailable.
	task, err := a.DAG.GetTask(ctx, taskID)
	if err != nil {
		logger.Warn("Beads writeback skipped: cannot resolve task project", "taskID", taskID, "error", err)
		return nil
	}
	bc, ok := a.BeadsClients[task.Project]
	if !ok {
		return nil
	}
	switch detail.Reason {
	case CloseCompleted:
		reason := fmt.Sprintf("Completed by CHUM. PR #%d", detail.PRNumber)
		if err := bc.Close(ctx, taskID, reason); err != nil {
			logger.Warn("Beads writeback failed", "taskID", taskID, "error", err)
		}
	case CloseDecomposed:
		if err := bc.Update(ctx, taskID, map[string]string{
			"status": types.StatusDecomposed,
		}); err != nil {
			logger.Warn("Beads decomposed writeback failed", "taskID", taskID, "error", err)
		}
	}
	return nil
}

// --- 7. CleanupWorktreeActivity ---

// CleanupWorktreeActivity removes the git worktree after the task completes.
func (a *Activities) CleanupWorktreeActivity(ctx context.Context, baseDir, wtDir string) error {
	return gitpkg.CleanupWorktree(ctx, baseDir, wtDir)
}

// --- context helpers ---

// buildCodebaseContext produces AST-based codebase context for the agent prompt.
// Falls back to file listing if AST parsing fails.
func (a *Activities) buildCodebaseContext(ctx context.Context, workDir string) string {
	if a.AST != nil {
		files, err := a.AST.ParseDir(ctx, workDir)
		if err == nil && len(files) > 0 {
			return astpkg.Summarize(files)
		}
		a.Logger.Warn("AST parse failed, falling back to file listing", "Error", err)
	}
	return fallbackFileList(ctx, workDir)
}

// fallbackFileList is the original file-listing approach used when AST parsing
// is unavailable or fails.
func fallbackFileList(ctx context.Context, workDir string) string {
	var sections []string

	cmd := exec.CommandContext(ctx, "go", "list", "./...")
	cmd.Dir = workDir
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		sections = append(sections, "Go packages:\n"+string(out))
	}

	cmd = exec.CommandContext(ctx, "find", ".", "-type", "f",
		"-not", "-path", "./.git/*",
		"-not", "-path", "./vendor/*",
		"-not", "-path", "./node_modules/*",
		"-not", "-name", "*.sum",
	)
	cmd.Dir = workDir
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		tree := string(out)
		if len(tree) > 4000 {
			tree = tree[:4000] + "\n... (truncated)"
		}
		sections = append(sections, "Files:\n"+tree)
	}

	if len(sections) == 0 {
		return "(could not determine project structure)"
	}
	return strings.Join(sections, "\n")
}

// --- helpers ---

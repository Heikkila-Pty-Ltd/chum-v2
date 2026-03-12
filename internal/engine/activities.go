package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"time"

	"go.temporal.io/sdk/activity"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beadsbridge"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	gitpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/git"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/notify"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/perf"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// Activities holds dependencies for Temporal activity methods.
type Activities struct {
	DAG          dag.TaskStore
	Config       *config.Config
	Logger       *slog.Logger
	AST          *astpkg.Parser
	BeadsClients map[string]beads.Store
	ChatSend     notify.ChatSender
	LLM          llm.Runner
	Traces       store.TraceStore // execution trace recording (nil = no-op)
	Perf         *perf.Tracker    // performance tracking (nil = no-op)
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

// SetupWorktreeFromRefActivity creates an isolated git worktree for a task
// branch, starting from a specific ref (for example a PR head SHA).
func (a *Activities) SetupWorktreeFromRefActivity(ctx context.Context, baseDir, taskID, startRef string) (string, error) {
	logger := activity.GetLogger(ctx)
	wtDir, err := gitpkg.SetupWorktreeAtRef(ctx, baseDir, taskID, startRef)
	if err != nil {
		return "", fmt.Errorf("setup worktree from ref: %w", err)
	}
	logger.Info("Worktree created from ref", "path", wtDir, "start_ref", startRef)
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
			out, err := runCommandCombinedOutput(ctx, req.WorkDir, parts...)
			if err != nil {
				// Empty-output failures are commonly transient/cancellation artifacts.
				// Retry once before failing hard.
				if strings.TrimSpace(out) == "" && ctx.Err() == nil {
					logger.Warn("Preflight baseline build failed with empty output; retrying once",
						"TaskID", req.TaskID,
						"Project", req.Project,
						"Command", buildCmd,
						"Error", err.Error(),
					)
					out, err = runCommandCombinedOutput(ctx, req.WorkDir, parts...)
				}
				if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					cancelErr := ctx.Err()
					if cancelErr == nil {
						cancelErr = err
					}
					return nil, fmt.Errorf("PREFLIGHT: baseline build cancelled while running %q: %w", buildCmd, cancelErr)
				}
				if strings.TrimSpace(out) == "" {
					return nil, fmt.Errorf("PREFLIGHT: project doesn't build before coding — %s failed with no output: %v",
						buildCmd, err)
				}
				return nil, fmt.Errorf("PREFLIGHT: project doesn't build before coding — %s failed: %s",
					buildCmd, types.Truncate(out, 300))
			}
			logger.Info("Preflight: baseline build OK")
		}
	}

	// Build AST codebase context (filtered by task relevance)
	codeContext := a.buildCodebaseContextForTask(ctx, req.WorkDir, req.Prompt)

	prompt := fmt.Sprintf(`You are a senior software engineer. Implement the following task.

%s

CODEBASE:
%s

Implement this task by modifying the necessary files. Do not explain, just code.`, wrapUserContent("TASK", req.Prompt), codeContext)

	result, err := a.LLM.Exec(ctx, req.Agent, req.Model, req.WorkDir, prompt)
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
		ExitCode:     result.ExitCode,
		Output:       result.Output,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUSD:      result.CostUSD,
	}, nil
}

func runCommandCombinedOutput(ctx context.Context, workDir string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("empty command args")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
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

// --- 5. CreatePRInfoActivity ---

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
	detail, err := closeTask(ctx, a.DAG, taskID, detail)
	if err != nil {
		return err
	}
	projectTaskToBeads(ctx, logger, a.DAG, a.Config, a.BeadsClients, taskID, detail)
	return nil
}

// closeTask handles DAG persistence: preserves existing PR metadata, marshals
// the close detail, and updates the task status + error_log. It returns the
// (potentially enriched) detail for downstream use.
func closeTask(ctx context.Context, store dag.TaskStore, taskID string, detail CloseDetail) (CloseDetail, error) {
	// Preserve existing PR linkage on follow-up failures where newer close
	// payloads omit PR metadata (e.g. exec_failed during resumed review).
	if detail.PRNumber == 0 || strings.TrimSpace(detail.ReviewURL) == "" {
		if current, err := store.GetTask(ctx, taskID); err == nil && strings.TrimSpace(current.ErrorLog) != "" {
			var prev CloseDetail
			if err := json.Unmarshal([]byte(current.ErrorLog), &prev); err == nil {
				if detail.PRNumber == 0 && prev.PRNumber > 0 {
					detail.PRNumber = prev.PRNumber
				}
				if strings.TrimSpace(detail.ReviewURL) == "" && strings.TrimSpace(prev.ReviewURL) != "" {
					detail.ReviewURL = prev.ReviewURL
				}
			}
		}
	}

	raw, err := json.Marshal(detail)
	if err != nil {
		return detail, fmt.Errorf("marshal close detail: %w", err)
	}
	if err := store.UpdateTask(ctx, taskID, map[string]any{
		"status":    string(detail.Reason),
		"error_log": string(raw),
	}); err != nil {
		return detail, fmt.Errorf("close task %s: %w", taskID, err)
	}
	return detail, nil
}

// projectTaskToBeads handles beads bridge outbox projection and legacy direct
// beads writeback. Best-effort: errors are logged but never returned.
func projectTaskToBeads(ctx context.Context, logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}, store dag.TaskStore, cfg *config.Config, beadsClients map[string]beads.Store, taskID string, detail CloseDetail) {
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		logger.Warn("Beads writeback skipped: cannot resolve task project", "taskID", taskID, "error", err)
		return
	}

	mappedIssueID := ""
	if bridgeDAG, ok := store.(*dag.DAG); ok {
		if mapping, mapErr := bridgeDAG.GetBeadsMappingByTask(ctx, task.Project, taskID); mapErr == nil {
			mappedIssueID = strings.TrimSpace(mapping.IssueID)
		}
	}

	// S6-S8: When bridge mode is enabled, writebacks are projected via durable outbox.
	if cfg != nil && cfg.BeadsBridge.Enabled && !cfg.BeadsBridge.DryRun {
		if bridgeDAG, ok := store.(*dag.DAG); ok {
			if mappedIssueID != "" {
				if detail.Reason != CloseDecomposed {
					if err := beadsbridge.EnqueueTaskTerminal(ctx, bridgeDAG, task.Project, mappedIssueID, taskID,
						string(detail.Reason), detail.SubReason, map[string]any{
							"category":   detail.Category,
							"summary":    detail.Summary,
							"review_url": detail.ReviewURL,
							"pr_number":  detail.PRNumber,
						}); err != nil {
						logger.Warn("Bridge outbox enqueue failed", "taskID", taskID, "issueID", mappedIssueID, "error", err)
					}
				}
				return
			}
		}
		logger.Info("Beads writeback skipped: bridge mode requires mapped beads issue",
			"taskID", taskID,
			"project", task.Project,
		)
		return
	}

	bc, ok := beadsClients[task.Project]
	if !ok {
		return
	}
	issueID := mappedIssueID
	if issueID == "" && task.Metadata != nil {
		issueID = strings.TrimSpace(task.Metadata["beads_issue_id"])
	}
	if issueID == "" {
		issueID = taskID
	}
	// In non-bridge mode, avoid noisy writeback failures for DAG-only synthetic tasks.
	if mappedIssueID == "" {
		if _, showErr := bc.Show(ctx, issueID); showErr != nil {
			logger.Info("Beads writeback skipped: task has no matching beads issue",
				"taskID", taskID,
				"project", task.Project,
				"issueID", issueID,
				"error", showErr,
			)
			return
		}
	}
	switch detail.Reason {
	case CloseCompleted:
		reason := fmt.Sprintf("Completed by CHUM. PR #%d", detail.PRNumber)
		if err := bc.Close(ctx, issueID, reason); err != nil {
			logger.Warn("Beads writeback failed", "taskID", taskID, "issueID", issueID, "error", err)
		}
	case CloseDecomposed:
		if err := bc.Update(ctx, issueID, map[string]string{
			"status": string(types.StatusDecomposed),
		}); err != nil {
			logger.Warn("Beads decomposed writeback failed", "taskID", taskID, "issueID", issueID, "error", err)
		}
	case CloseDoDFailed:
		if err := bc.Update(ctx, issueID, map[string]string{
			"status": string(types.StatusDoDFailed),
		}); err != nil {
			logger.Warn("Beads dod_failed writeback failed", "taskID", taskID, "issueID", issueID, "error", err)
		}
	case CloseFailed:
		if err := bc.Update(ctx, issueID, map[string]string{
			"status": string(types.StatusFailed),
		}); err != nil {
			logger.Warn("Beads failed writeback failed", "taskID", taskID, "issueID", issueID, "error", err)
		}
	}
}

// --- 7. CleanupWorktreeActivity ---

// CleanupWorktreeActivity removes the git worktree after the task completes.
func (a *Activities) CleanupWorktreeActivity(ctx context.Context, baseDir, wtDir string) error {
	return gitpkg.CleanupWorktree(ctx, baseDir, wtDir)
}

// --- context helpers ---

// buildCodebaseContextForTask produces AST-based codebase context filtered by
// relevance to the given task prompt. When taskPrompt is non-empty, files are
// scored against it: relevant files get full source detail while the rest get
// signatures only. This dramatically reduces token usage.
func (a *Activities) buildCodebaseContextForTask(ctx context.Context, workDir, taskPrompt string) string {
	if a.AST != nil {
		files, err := a.AST.ParseDir(ctx, workDir)
		if err == nil && len(files) > 0 {
			if taskPrompt != "" {
				ef := astpkg.NewEmbedFilter()
				relevant, surrounding := ef.FilterRelevantByEmbedding(ctx, taskPrompt, files)
				if len(relevant) > 0 {
					a.Logger.Info("Targeted context injection (embedding)",
						"relevant", len(relevant),
						"surrounding", len(surrounding),
						"total", len(files))
					return astpkg.SummarizeTargeted(surrounding, relevant)
				}
			}
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

// --- 8. RecordTraceActivity ---

// TraceOutcome captures the result of an AgentWorkflow for trace recording.
type TraceOutcome struct {
	TaskID       string
	SessionID    string // Temporal workflow run ID
	Agent        string
	Model        string
	Tier         string
	Reason       string // CloseCompleted, CloseDoDFailed, etc.
	SubReason    string
	Duration     time.Duration
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// rewardForReason maps close reasons to terminal reward values.
func rewardForReason(reason CloseReason) float64 {
	switch reason {
	case CloseCompleted:
		return 1.0
	case CloseDecomposed:
		return 0.5
	default:
		return -1.0
	}
}

// isPerfRelevant returns true if the outcome reflects provider execution quality
// rather than infrastructure/review noise. Only execution-related outcomes should
// feed UCT provider ranking.
func isPerfRelevant(subReason string) bool {
	switch subReason {
	// Direct execution outcomes.
	case "completed", "exec_failed", "dod_failed", "dod_error",
		"decomposed", "decompose_failed":
		return true
	// DoD failure categories from ClassifyFailure (all reflect code quality).
	case "test_failure", "compile_error", "lint_error", "lint_config_error",
		"timeout", "activity_timeout", "scope_drift", "execution_error",
		"dod_check_failed":
		return true
	default:
		return false
	}
}

// RecordTraceActivity writes execution trace and perf data for a completed workflow.
// Best-effort: errors are logged but do not fail the workflow.
//
// Traces are always recorded (full workflow history). Perf runs are only recorded
// for execution-relevant outcomes to avoid polluting UCT provider ranking with
// infrastructure/review noise.
func (a *Activities) RecordTraceActivity(ctx context.Context, outcome TraceOutcome) error {
	logger := activity.GetLogger(ctx)

	success := outcome.Reason == string(CloseCompleted)
	successCount := 0
	if success {
		successCount = 1
	}
	reward := rewardForReason(CloseReason(outcome.Reason))

	// Record execution trace (always — full workflow history).
	if a.Traces != nil {
		traceID, err := a.Traces.StartExecutionTrace(outcome.TaskID, outcome.Agent, "")
		if err != nil {
			logger.Error("Failed to start execution trace", "error", err)
		} else {
			if err := a.Traces.AppendTraceEvent(traceID, store.TraceEvent{
				Stage:        outcome.Reason,
				Step:         outcome.SubReason,
				Tool:         outcome.Agent,
				DurationMs:   outcome.Duration.Milliseconds(),
				Success:      success,
				ErrorContext: outcome.SubReason,
			}); err != nil {
				logger.Error("Failed to append trace event", "error", err)
			}
			if err := a.Traces.CompleteExecutionTrace(traceID, outcome.Reason, outcome.SubReason, 1, successCount); err != nil {
				logger.Error("Failed to complete execution trace", "error", err)
			}
		}

		// Backpropagate reward to any graph trace events for this session.
		if outcome.SessionID != "" {
			if err := a.Traces.BackpropagateReward(ctx, outcome.SessionID, reward); err != nil {
				logger.Error("Failed to backpropagate reward", "error", err)
			}
		}
	}

	// Record perf run only for execution-relevant outcomes.
	// Infra/review failures (push, PR, merge, reviewer) are not the provider's fault.
	// Decomposed is neutral — the provider correctly identified work needed splitting.
	if a.Perf != nil && isPerfRelevant(outcome.SubReason) {
		perfSuccess := success || outcome.Reason == string(CloseDecomposed)
		if err := a.Perf.Record(ctx, outcome.Agent, outcome.Model, outcome.Tier, perfSuccess, outcome.Duration.Seconds(), outcome.InputTokens, outcome.OutputTokens, outcome.CostUSD); err != nil {
			logger.Error("Failed to record perf run", "error", err)
		}
	}

	logger.Info("Trace recorded",
		"task", outcome.TaskID,
		"reason", outcome.Reason,
		"reward", reward,
		"duration", outcome.Duration,
	)
	return nil
}

// --- helpers ---

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/Heikkila-Pty-Ltd/chum/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum/internal/dag"
	gitpkg "github.com/Heikkila-Pty-Ltd/chum/internal/git"
)

// Activities holds dependencies for Temporal activity methods.
type Activities struct {
	DAG    *dag.DAG
	Config *config.Config
	Logger *slog.Logger
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

// --- 2. PlanActivity ---

// PlanActivity asks an LLM to produce a structured plan from the task prompt.
func (a *Activities) PlanActivity(ctx context.Context, req TaskRequest) (*Plan, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Generating plan", "TaskID", req.TaskID, "Agent", req.Agent)

	prompt := fmt.Sprintf(`You are a senior software engineer. Analyze this task and produce a JSON plan.

TASK: %s

Respond with ONLY a JSON object in this exact format:
{
  "summary": "one-line description of what you'll do",
  "steps": ["step 1", "step 2", ...],
  "files_to_modify": ["path/to/file1.go", ...],
  "acceptance_criteria": ["criterion 1", ...]
}`, req.Prompt)

	result, err := RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("run CLI: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("CLI exited %d: %s", result.ExitCode, truncate(result.Output, 500))
	}

	jsonStr := ExtractJSON(result.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON in plan output: %s", truncate(result.Output, 500))
	}

	var plan Plan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, fmt.Errorf("parse plan JSON: %w", err)
	}

	if issues := plan.Validate(); len(issues) > 0 {
		return nil, fmt.Errorf("plan validation failed: %s", strings.Join(issues, "; "))
	}

	logger.Info("Plan ready", "Summary", truncate(plan.Summary, 120), "Steps", len(plan.Steps))
	return &plan, nil
}

// --- 3. ExecuteActivity ---

// ExecuteActivity runs the LLM CLI to implement the plan.
func (a *Activities) ExecuteActivity(ctx context.Context, plan Plan, req TaskRequest) (*ExecResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Executing plan", "TaskID", req.TaskID, "Agent", req.Agent)

	prompt := fmt.Sprintf(`You are a senior software engineer. Implement the following plan.

PLAN: %s

STEPS:
%s

FILES TO MODIFY: %s

ACCEPTANCE CRITERIA:
%s

Implement this plan by modifying the necessary files. Do not explain, just code.`,
		plan.Summary,
		strings.Join(plan.Steps, "\n"),
		strings.Join(plan.FilesToModify, ", "),
		strings.Join(plan.Acceptance, "\n"),
	)

	result, err := RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("execute CLI: %w", err)
	}

	logger.Info("Execution complete", "ExitCode", result.ExitCode)
	return &ExecResult{
		ExitCode: result.ExitCode,
		Output:   result.Output,
	}, nil
}

// --- 4. DoDCheckActivity ---

// DoDCheckActivity runs DoD verification checks (build, test, vet).
func (a *Activities) DoDCheckActivity(ctx context.Context, workDir, project string) (*gitpkg.DoDResult, error) {
	logger := activity.GetLogger(ctx)

	// Find project config
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

// --- 5. PushActivity ---

// PushActivity pushes the feature branch to origin.
func (a *Activities) PushActivity(ctx context.Context, workDir string) error {
	return gitpkg.Push(ctx, workDir)
}

// --- 6. CreatePRActivity ---

// CreatePRActivity creates a pull request for the feature branch.
func (a *Activities) CreatePRActivity(ctx context.Context, workDir, title string) error {
	return gitpkg.CreatePR(ctx, workDir, title)
}

// --- 7. CloseTaskActivity ---

// CloseTaskActivity sets the task status in the DAG (e.g. "completed", "dod_failed").
func (a *Activities) CloseTaskActivity(ctx context.Context, taskID, status string) error {
	return a.DAG.CloseTask(ctx, taskID, status)
}

// --- 8. CleanupWorktreeActivity ---

// CleanupWorktreeActivity removes the git worktree after the task completes.
func (a *Activities) CleanupWorktreeActivity(ctx context.Context, baseDir, wtDir string) error {
	return gitpkg.CleanupWorktree(ctx, baseDir, wtDir)
}

// --- helpers ---

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

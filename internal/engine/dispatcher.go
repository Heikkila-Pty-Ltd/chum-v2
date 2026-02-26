package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// DispatcherWorkflow scans the DAG for ready tasks and spawns AgentWorkflow
// children. Designed to run on a Temporal Schedule (every tick_interval).
func DispatcherWorkflow(ctx workflow.Context, _ struct{}) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("DispatcherWorkflow tick")

	// Activity options for scanning
	scanOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	var da *DispatchActivities
	scanCtx := workflow.WithActivityOptions(ctx, scanOpts)

	var candidates []DispatchCandidate
	if err := workflow.ExecuteActivity(scanCtx, da.ScanCandidatesActivity).Get(ctx, &candidates); err != nil {
		logger.Error("Scan failed", "error", err)
		return fmt.Errorf("scan: %w", err)
	}

	if len(candidates) == 0 {
		logger.Info("No ready tasks")
		return nil
	}

	logger.Info("Found candidates", "count", len(candidates))

	// Spawn AgentWorkflow for each candidate
	for _, c := range candidates {
		// Mark task as "running" BEFORE spawning child to prevent double-dispatch
		markCtx := workflow.WithActivityOptions(ctx, scanOpts)
		if err := workflow.ExecuteActivity(markCtx, da.MarkTaskRunningActivity, c.TaskID).Get(ctx, nil); err != nil {
			logger.Error("Failed to mark task running, skipping", "TaskID", c.TaskID, "error", err)
			continue
		}

		childOpts := workflow.ChildWorkflowOptions{
			WorkflowID:               fmt.Sprintf("chum-agent-%s", c.TaskID),
			WorkflowExecutionTimeout: 2 * time.Hour,
			ParentClosePolicy:        enums.PARENT_CLOSE_POLICY_ABANDON,
		}
		childCtx := workflow.WithChildOptions(ctx, childOpts)

		req := TaskRequest(c)

		// Wait for child workflow to actually start — without this,
		// the parent completes before the server creates the child
		childFuture := workflow.ExecuteChildWorkflow(childCtx, AgentWorkflow, req)
		var childExecution workflow.Execution
		if err := childFuture.GetChildWorkflowExecution().Get(ctx, &childExecution); err != nil {
			logger.Error("Failed to start agent workflow", "TaskID", c.TaskID, "error", err)
			continue
		}
		logger.Info("Dispatched agent", "TaskID", c.TaskID, "Agent", c.Agent, "ChildWorkflowID", childExecution.ID)
	}

	return nil
}

// DispatchCandidate is a ready task that should be dispatched.
type DispatchCandidate struct {
	TaskID   string
	Project  string
	Prompt   string
	WorkDir  string
	Agent    string
	Model    string
	ParentID string
}

// DispatchActivities holds dependencies for dispatch-related activities.
type DispatchActivities struct {
	DAG    *dag.DAG
	Config *config.Config
	Logger *slog.Logger
}

// MarkTaskRunningActivity marks a task as "running" in the DAG.
// Called before spawning the child workflow to prevent double-dispatch.
func (da *DispatchActivities) MarkTaskRunningActivity(ctx context.Context, taskID string) error {
	return da.DAG.UpdateTaskStatus(ctx, taskID, "running")
}

// ScanCandidatesActivity discovers ready tasks across all enabled projects.
func (da *DispatchActivities) ScanCandidatesActivity(ctx context.Context) ([]DispatchCandidate, error) {
	var candidates []DispatchCandidate

	for projectName, project := range da.Config.Projects {
		if !project.Enabled {
			continue
		}

		// Pull latest master so agents start from current code
		pullMaster(ctx, project.Workspace, da.Logger)

		tasks, err := da.DAG.GetReadyNodes(ctx, projectName)
		if err != nil {
			return nil, fmt.Errorf("get ready nodes for %s: %w", projectName, err)
		}

		// Cap per project
		max := da.Config.General.MaxConcurrent
		if len(tasks) > max {
			tasks = tasks[:max]
		}

		// Pick the first enabled provider
		agent, model := pickProvider(da.Config)

		for _, t := range tasks {
			prompt := t.Description
			if t.Acceptance != "" {
				prompt += "\n\nAcceptance Criteria:\n" + t.Acceptance
			}

			candidates = append(candidates, DispatchCandidate{
				TaskID:   t.ID,
				Project:  projectName,
				Prompt:   prompt,
				WorkDir:  project.Workspace,
				Agent:    agent,
				Model:    model,
				ParentID: t.ParentID,
			})
		}
	}

	return candidates, nil
}

// pullMaster fetches and fast-forwards master so agents start from the latest code.
// Non-fatal — if it fails, we proceed with whatever we have.
func pullMaster(ctx context.Context, workDir string, logger *slog.Logger) {
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin", "master")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		// Try "main" if "master" doesn't exist
		cmd = exec.CommandContext(ctx, "git", "fetch", "origin", "main")
		cmd.Dir = workDir
		if out2, err2 := cmd.CombinedOutput(); err2 != nil {
			logger.Warn("Failed to fetch from origin",
				"WorkDir", workDir,
				"Error", strings.TrimSpace(string(out)),
				"Error2", strings.TrimSpace(string(out2)))
			return
		}
	}
	// Fast-forward the local branch to match origin
	cmd = exec.CommandContext(ctx, "git", "merge", "--ff-only", "FETCH_HEAD")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.Warn("Failed to fast-forward master",
			"WorkDir", workDir,
			"Output", strings.TrimSpace(string(out)))
	} else {
		logger.Info("Pulled latest from origin", "WorkDir", workDir)
	}
}

func pickProvider(cfg *config.Config) (cli, model string) {
	for _, p := range cfg.Providers {
		if p.Enabled {
			return p.CLI, p.Model
		}
	}
	return "claude", "" // fallback
}

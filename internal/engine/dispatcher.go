package engine

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/Heikkila-Pty-Ltd/chum/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum/internal/dag"
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
		childOpts := workflow.ChildWorkflowOptions{
			WorkflowID:                 fmt.Sprintf("chum-agent-%s", c.TaskID),
			WorkflowExecutionTimeout:   2 * time.Hour,
			ParentClosePolicy:          enums.PARENT_CLOSE_POLICY_ABANDON,
		}
		childCtx := workflow.WithChildOptions(ctx, childOpts)

		req := TaskRequest{
			TaskID:  c.TaskID,
			Project: c.Project,
			Prompt:  c.Prompt,
			WorkDir: c.WorkDir,
			Agent:   c.Agent,
			Model:   c.Model,
		}

		// Fire-and-forget — the child runs independently
		workflow.ExecuteChildWorkflow(childCtx, AgentWorkflow, req)
		logger.Info("Dispatched agent", "TaskID", c.TaskID, "Agent", c.Agent)
	}

	return nil
}

// DispatchCandidate is a ready task that should be dispatched.
type DispatchCandidate struct {
	TaskID  string
	Project string
	Prompt  string
	WorkDir string
	Agent   string
	Model   string
}

// DispatchActivities holds dependencies for dispatch-related activities.
type DispatchActivities struct {
	DAG    *dag.DAG
	Config *config.Config
}

// ScanCandidatesActivity discovers ready tasks across all enabled projects.
func (da *DispatchActivities) ScanCandidatesActivity(ctx context.Context) ([]DispatchCandidate, error) {
	var candidates []DispatchCandidate

	for projectName, project := range da.Config.Projects {
		if !project.Enabled {
			continue
		}

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
				TaskID:  t.ID,
				Project: projectName,
				Prompt:  prompt,
				WorkDir: project.Workspace,
				Agent:   agent,
				Model:   model,
			})
		}
	}

	return candidates, nil
}

func pickProvider(cfg *config.Config) (cli, model string) {
	for _, p := range cfg.Providers {
		if p.Enabled {
			return p.CLI, p.Model
		}
	}
	return "claude", "" // fallback
}

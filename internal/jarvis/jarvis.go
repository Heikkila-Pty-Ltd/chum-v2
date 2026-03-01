// Package jarvis provides the integration layer between the Jarvis agent
// and the CHUM execution engine. Jarvis submits work items (decisions,
// code changes, investigations) and CHUM handles worktree isolation,
// execution, DoD checks, PR creation, and review.
//
// This is Jarvis's execution engine — instead of running code changes
// inline, Jarvis decomposes work into tasks that CHUM's Temporal
// workflows execute with full auditability.
package jarvis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// WorkRequest is what Jarvis submits when it wants code executed.
type WorkRequest struct {
	// Title is a short description (used for task tracking and PR title).
	Title string `json:"title"`

	// Description is the full prompt/context for the LLM agent.
	Description string `json:"description"`

	// Project is the CHUM project key (must match chum.toml).
	Project string `json:"project"`

	// Agent overrides the default LLM (claude, gemini, codex). Empty = default.
	Agent string `json:"agent,omitempty"`

	// Priority controls ordering. Lower = higher priority.
	Priority int `json:"priority,omitempty"`

	// Labels for categorization and filtering.
	Labels []string `json:"labels,omitempty"`

	// Source tracks where this request originated (e.g. "jarvis-initiative",
	// "jarvis-goal-5", "jarvis-observation").
	Source string `json:"source,omitempty"`

	// ParentTaskID links subtasks for decomposed work.
	ParentTaskID string `json:"parent_task_id,omitempty"`

	// Timeout overrides the default execution timeout.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// WorkResult is returned when a work request completes.
type WorkResult struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"` // ready, running, completed, failed, needs_review, decomposed
	PRNumber int    `json:"pr_number,omitempty"`
	ReviewURL string `json:"review_url,omitempty"`
	Error    string `json:"error,omitempty"`
	SubTasks []string `json:"sub_tasks,omitempty"`
}

// Engine is the Jarvis-CHUM integration. It wraps the DAG and Temporal
// client to provide a clean API for submitting and tracking work.
type Engine struct {
	dag       dag.TaskStore
	temporal  client.Client
	taskQueue string
	workDirs  map[string]string // project → workspace path
	logger    *slog.Logger
}

// NewEngine creates a Jarvis execution engine.
func NewEngine(d dag.TaskStore, tc client.Client, taskQueue string, workDirs map[string]string, logger *slog.Logger) *Engine {
	return &Engine{
		dag:       d,
		temporal:  tc,
		taskQueue: taskQueue,
		workDirs:  workDirs,
		logger:    logger,
	}
}

// Submit creates a task in the DAG as "ready". The task will be picked up
// by CHUM's dispatcher on its next tick (usually within 2 minutes).
// Returns the task ID.
func (e *Engine) Submit(ctx context.Context, req WorkRequest) (string, error) {
	workDir := e.workDirs[req.Project]
	if workDir == "" {
		return "", fmt.Errorf("unknown project %q — not in CHUM config", req.Project)
	}

	source := req.Source
	if source == "" {
		source = "jarvis"
	}

	labels := req.Labels
	if labels == nil {
		labels = []string{}
	}
	// Tag all Jarvis-submitted tasks.
	labels = append(labels, "jarvis-submitted")

	task := dag.Task{
		Title:       req.Title,
		Description: req.Description,
		Status:      types.StatusReady,
		Project:     req.Project,
		Labels:      labels,
		ParentID:    req.ParentTaskID,
		Metadata:    map[string]string{"source": source},
	}

	if req.Priority > 0 {
		task.Priority = req.Priority
	}

	id, err := e.dag.CreateTask(ctx, task)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	e.logger.Info("Jarvis submitted task",
		"task_id", id,
		"project", req.Project,
		"title", req.Title,
		"source", source,
	)

	return id, nil
}

// SubmitAndDispatch creates a task and immediately triggers the dispatcher
// workflow to pick it up, bypassing the tick interval. Use for urgent work.
func (e *Engine) SubmitAndDispatch(ctx context.Context, req WorkRequest) (string, error) {
	id, err := e.Submit(ctx, req)
	if err != nil {
		return "", err
	}

	if err := e.TriggerDispatch(ctx); err != nil {
		// Task is still in the DAG — it'll be picked up on the next tick.
		e.logger.Warn("Immediate dispatch trigger failed, task will be picked up on next tick",
			"task_id", id, "error", err)
	}

	return id, nil
}

// TriggerDispatch starts a one-off dispatcher workflow run. This causes
// the dispatcher to scan for ready tasks (including just-submitted ones)
// immediately rather than waiting for the next scheduled tick.
func (e *Engine) TriggerDispatch(ctx context.Context) error {
	if e.temporal == nil {
		return fmt.Errorf("temporal client not available")
	}

	wfOpts := client.StartWorkflowOptions{
		ID:                       fmt.Sprintf("jarvis-dispatch-trigger-%d", time.Now().UnixNano()),
		TaskQueue:                e.taskQueue,
		WorkflowExecutionTimeout: 5 * time.Minute,
	}

	// Start DispatcherWorkflow by registered name. The workflow is registered
	// as "DispatcherWorkflow" by the engine package.
	run, err := e.temporal.ExecuteWorkflow(ctx, wfOpts, "DispatcherWorkflow", struct{}{})
	if err != nil {
		return fmt.Errorf("trigger dispatch: %w", err)
	}

	e.logger.Info("Triggered immediate dispatch",
		"workflow_id", run.GetID(),
		"run_id", run.GetRunID(),
	)
	return nil
}

// GetStatus returns the current status of a Jarvis-submitted task.
func (e *Engine) GetStatus(ctx context.Context, taskID string) (WorkResult, error) {
	task, err := e.dag.GetTask(ctx, taskID)
	if err != nil {
		return WorkResult{}, fmt.Errorf("get task %s: %w", taskID, err)
	}

	result := WorkResult{
		TaskID: taskID,
		Status: task.Status,
	}

	// Parse error_log for close details if task is done.
	if task.ErrorLog != "" {
		var detail struct {
			PRNumber  int    `json:"pr_number"`
			ReviewURL string `json:"review_url"`
			SubReason string `json:"sub_reason"`
		}
		if json.Unmarshal([]byte(task.ErrorLog), &detail) == nil {
			result.PRNumber = detail.PRNumber
			result.ReviewURL = detail.ReviewURL
			if task.Status != string(types.StatusCompleted) {
				result.Error = detail.SubReason
			}
		}
	}

	return result, nil
}

// ListPending returns all Jarvis-submitted tasks that haven't completed yet.
func (e *Engine) ListPending(ctx context.Context, project string) ([]WorkResult, error) {
	tasks, err := e.dag.ListTasks(ctx, project, types.StatusReady, types.StatusRunning)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	var results []WorkResult
	for _, t := range tasks {
		// Filter to Jarvis-submitted only.
		isJarvis := false
		for _, l := range t.Labels {
			if l == "jarvis-submitted" {
				isJarvis = true
				break
			}
		}
		if !isJarvis {
			continue
		}
		results = append(results, WorkResult{
			TaskID: t.ID,
			Status: t.Status,
		})
	}

	return results, nil
}

// WaitForResult blocks until the task reaches a terminal status,
// then returns the result. Use with a context timeout.
func (e *Engine) WaitForResult(ctx context.Context, taskID string) (WorkResult, error) {
	// If Temporal client available, try to find the agent workflow.
	if e.temporal != nil {
		wfID := fmt.Sprintf("chum-agent-%s", taskID)
		desc, err := e.temporal.DescribeWorkflowExecution(ctx, wfID, "")
		if err == nil {
			st := desc.WorkflowExecutionInfo.Status
			if st == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED ||
				st == enums.WORKFLOW_EXECUTION_STATUS_FAILED ||
				st == enums.WORKFLOW_EXECUTION_STATUS_TERMINATED ||
				st == enums.WORKFLOW_EXECUTION_STATUS_CANCELED {
				return e.GetStatus(ctx, taskID)
			}
		}
	}

	// Poll the DAG directly for terminal status.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return WorkResult{}, ctx.Err()
		case <-ticker.C:
			result, err := e.GetStatus(ctx, taskID)
			if err != nil {
				continue
			}
			switch result.Status {
			case "completed", "failed", "needs_review", "dod_failed", "decomposed":
				return result, nil
			}
		}
	}
}

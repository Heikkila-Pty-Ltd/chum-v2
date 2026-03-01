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

	// Callback is an optional webhook URL to POST results to.
	Callback string `json:"callback,omitempty"`

	// Timeout overrides the default execution timeout.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// WorkResult is returned when a work request completes.
type WorkResult struct {
	TaskID    string         `json:"task_id"`
	Status    string         `json:"status"` // completed, failed, needs_review, decomposed
	PRNumber  int            `json:"pr_number,omitempty"`
	ReviewURL string         `json:"review_url,omitempty"`
	Error     string         `json:"error,omitempty"`
	Duration  time.Duration  `json:"duration,omitempty"`
	SubTasks  []string       `json:"sub_tasks,omitempty"`
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

// Submit creates a task in the DAG and optionally triggers immediate dispatch.
// Returns the task ID. The task will be picked up by the dispatcher on its
// next tick, or immediately if dispatch=true.
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

// SubmitAndDispatch creates a task and immediately starts the agent workflow,
// bypassing the dispatcher tick interval. Use for urgent work.
func (e *Engine) SubmitAndDispatch(ctx context.Context, req WorkRequest) (string, error) {
	id, err := e.Submit(ctx, req)
	if err != nil {
		return "", err
	}

	if err := e.Dispatch(ctx, id, req); err != nil {
		return id, fmt.Errorf("task %s created but dispatch failed: %w", id, err)
	}

	return id, nil
}

// Dispatch starts the agent workflow for an existing task immediately.
func (e *Engine) Dispatch(ctx context.Context, taskID string, req WorkRequest) error {
	workDir := e.workDirs[req.Project]
	if workDir == "" {
		return fmt.Errorf("unknown project %q", req.Project)
	}

	// Mark task as running to prevent double-dispatch.
	if err := e.dag.UpdateTaskStatus(ctx, taskID, types.StatusRunning); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}

	agent := req.Agent
	if agent == "" {
		agent = "claude"
	}

	execTimeout := req.Timeout
	if execTimeout <= 0 {
		execTimeout = 45 * time.Minute
	}

	wfOpts := client.StartWorkflowOptions{
		ID:                       fmt.Sprintf("jarvis-agent-%s", taskID),
		TaskQueue:                e.taskQueue,
		WorkflowExecutionTimeout: 2 * time.Hour,
	}

	// Use JarvisAgentWorkflow which wraps AgentWorkflow with result tracking.
	wfReq := JarvisTaskRequest{
		TaskID:      taskID,
		Project:     req.Project,
		Prompt:      req.Description,
		WorkDir:     workDir,
		Agent:       agent,
		ExecTimeout: execTimeout,
		Source:      req.Source,
		Callback:    req.Callback,
	}

	run, err := e.temporal.ExecuteWorkflow(ctx, wfOpts, JarvisAgentWorkflow, wfReq)
	if err != nil {
		// Revert status on dispatch failure.
		_ = e.dag.UpdateTaskStatus(ctx, taskID, types.StatusReady)
		return fmt.Errorf("start workflow: %w", err)
	}

	e.logger.Info("Jarvis dispatched agent",
		"task_id", taskID,
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
	if task.Status == types.StatusCompleted || task.Status == types.StatusFailed {
		if task.ErrorLog != "" {
			var detail struct {
				PRNumber  int    `json:"pr_number"`
				ReviewURL string `json:"review_url"`
				SubReason string `json:"sub_reason"`
			}
			if json.Unmarshal([]byte(task.ErrorLog), &detail) == nil {
				result.PRNumber = detail.PRNumber
				result.ReviewURL = detail.ReviewURL
				if task.Status == types.StatusFailed {
					result.Error = detail.SubReason
				}
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

// WaitForResult blocks until the workflow for the given task completes,
// then returns the result. Use with a context timeout.
func (e *Engine) WaitForResult(ctx context.Context, taskID string) (WorkResult, error) {
	wfID := fmt.Sprintf("jarvis-agent-%s", taskID)

	// Describe the workflow to get the run ID.
	desc, err := e.temporal.DescribeWorkflowExecution(ctx, wfID, "")
	if err != nil {
		return WorkResult{}, fmt.Errorf("describe workflow %s: %w", wfID, err)
	}

	status := desc.WorkflowExecutionInfo.Status
	if status == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		return e.GetStatus(ctx, taskID)
	}
	if status == enums.WORKFLOW_EXECUTION_STATUS_FAILED ||
		status == enums.WORKFLOW_EXECUTION_STATUS_TERMINATED ||
		status == enums.WORKFLOW_EXECUTION_STATUS_CANCELED {
		return WorkResult{
			TaskID: taskID,
			Status: "failed",
			Error:  fmt.Sprintf("workflow %s", status.String()),
		}, nil
	}

	// Workflow still running — poll until done.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return WorkResult{}, ctx.Err()
		case <-ticker.C:
			desc, err := e.temporal.DescribeWorkflowExecution(ctx, wfID, "")
			if err != nil {
				continue
			}
			st := desc.WorkflowExecutionInfo.Status
			if st == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
				return e.GetStatus(ctx, taskID)
			}
			if st == enums.WORKFLOW_EXECUTION_STATUS_FAILED ||
				st == enums.WORKFLOW_EXECUTION_STATUS_TERMINATED ||
				st == enums.WORKFLOW_EXECUTION_STATUS_CANCELED {
				return WorkResult{
					TaskID: taskID,
					Status: "failed",
					Error:  fmt.Sprintf("workflow %s", st.String()),
				}, nil
			}
		}
	}
}

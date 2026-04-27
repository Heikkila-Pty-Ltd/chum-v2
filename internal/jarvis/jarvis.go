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
	"strings"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beadsbridge"
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

	// ExternalRef is an optional upstream correlation key (ticket ID, webhook ID).
	// This is distinct from beads issue IDs and DAG task IDs.
	ExternalRef string `json:"external_ref,omitempty"`

	// ParentTaskID links subtasks for decomposed work.
	ParentTaskID string `json:"parent_task_id,omitempty"`

	// Timeout overrides the default execution timeout.
	Timeout time.Duration `json:"timeout,omitempty"`

	// CallbackURL is an optional URL to POST results to when the task completes.
	// Used by Kaikki to receive results back via webhook.
	CallbackURL string `json:"callback_url,omitempty"`
}

// WorkResult is returned when a work request completes.
type WorkResult struct {
	TaskID    string   `json:"task_id"`
	Status    string   `json:"status"` // ready, running, completed, failed, needs_review, decomposed
	PRNumber  int      `json:"pr_number,omitempty"`
	ReviewURL string   `json:"review_url,omitempty"`
	Error     string   `json:"error,omitempty"`
	SubTasks  []string `json:"sub_tasks,omitempty"`
}

// Engine is the Jarvis-CHUM integration. It wraps the DAG and Temporal
// client to provide a clean API for submitting and tracking work.
type Engine struct {
	dag       dag.TaskStore
	temporal  client.Client
	taskQueue string
	workDirs  map[string]string // project → workspace path
	logger    *slog.Logger

	ingressPolicy string
	canaryLabel   string
	beadsClients  map[string]beads.Store
}

// NewEngine creates a Jarvis execution engine.
func NewEngine(d dag.TaskStore, tc client.Client, taskQueue string, workDirs map[string]string, logger *slog.Logger) *Engine {
	return &Engine{
		dag:       d,
		temporal:  tc,
		taskQueue: taskQueue,
		workDirs:  workDirs,
		logger:    logger,

		ingressPolicy: "legacy",
	}
}

// WorkDir returns the workspace path for a project, or empty string if unknown.
func (e *Engine) WorkDir(project string) string {
	return e.workDirs[project]
}

// ConfigureBeadsIngress configures beads-first ingress for external submissions.
// Any non-legacy policy requires Submit to create work in beads before DAG admission.
func (e *Engine) ConfigureBeadsIngress(policy, canaryLabel string, clients map[string]beads.Store) {
	p := strings.ToLower(strings.TrimSpace(policy))
	if p == "" {
		p = "legacy"
	}
	e.ingressPolicy = p
	e.canaryLabel = strings.TrimSpace(canaryLabel)
	e.beadsClients = clients
}

func (e *Engine) ingressRequiresBeads() bool {
	p := strings.ToLower(strings.TrimSpace(e.ingressPolicy))
	return p != "" && p != "legacy"
}

// BeadsClient returns the beads store for a project, or nil if unavailable.
func (e *Engine) BeadsClient(project string) beads.Store {
	return e.beadsClients[project]
}

// CanSubmitViaBeads reports whether Submit can route the given project through
// beads-first ingress under the current policy/configuration.
func (e *Engine) CanSubmitViaBeads(project string) bool {
	if !e.ingressRequiresBeads() {
		return false
	}
	return e.beadsClients[project] != nil
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

	labels := make([]string, 0, len(req.Labels)+1)
	seenLabels := map[string]bool{}
	for _, raw := range req.Labels {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		if seenLabels[key] {
			continue
		}
		seenLabels[key] = true
		labels = append(labels, label)
	}
	if !seenLabels["jarvis-submitted"] {
		labels = append(labels, "jarvis-submitted")
	}

	metadata := map[string]string{
		"source": source,
	}
	if externalRef := strings.TrimSpace(req.ExternalRef); externalRef != "" {
		metadata["external_ref"] = externalRef
	}
	if callbackURL := strings.TrimSpace(req.CallbackURL); callbackURL != "" {
		metadata["callback_url"] = callbackURL
	}

	if e.ingressRequiresBeads() {
		return e.submitViaBeads(ctx, req, labels, metadata)
	}

	task := dag.Task{
		Title:       req.Title,
		Description: req.Description,
		Status:      string(types.StatusReady),
		Project:     req.Project,
		Labels:      labels,
		ParentID:    req.ParentTaskID,
		Metadata:    metadata,
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

func (e *Engine) submitViaBeads(ctx context.Context, req WorkRequest, labels []string, metadata map[string]string) (string, error) {
	bc := e.beadsClients[req.Project]
	if bc == nil {
		return "", fmt.Errorf("beads ingress policy %q requires a beads client for project %q", e.ingressPolicy, req.Project)
	}
	bridgeDAG, ok := e.dag.(*dag.DAG)
	if !ok {
		return "", fmt.Errorf("beads ingress policy %q requires concrete DAG backend for project %q", e.ingressPolicy, req.Project)
	}

	issueLabels := append([]string(nil), labels...)
	if canary := strings.TrimSpace(e.canaryLabel); canary != "" {
		seen := false
		for _, label := range issueLabels {
			if strings.EqualFold(strings.TrimSpace(label), canary) {
				seen = true
				break
			}
		}
		if !seen {
			issueLabels = append(issueLabels, canary)
		}
	}

	parentIssueID := strings.TrimSpace(req.ParentTaskID)
	if parentIssueID != "" {
		if mapping, err := bridgeDAG.GetBeadsMappingByTask(ctx, req.Project, parentIssueID); err == nil {
			if mapped := strings.TrimSpace(mapping.IssueID); mapped != "" {
				parentIssueID = mapped
			}
		} else if !dag.IsNoRows(err) {
			return "", fmt.Errorf("resolve parent mapping for %s: %w", parentIssueID, err)
		}
	}

	priority := -1
	if req.Priority > 0 {
		priority = req.Priority
	}

	issueID, err := bc.Create(ctx, beads.CreateParams{
		Title:       req.Title,
		Description: req.Description,
		IssueType:   "task",
		Priority:    priority,
		Labels:      issueLabels,
		ParentID:    parentIssueID,
	})
	if err != nil {
		return "", fmt.Errorf("create beads issue: %w", err)
	}
	if err := bc.Update(ctx, issueID, map[string]string{"status": string(types.StatusReady)}); err != nil {
		return "", fmt.Errorf("mark beads issue %s ready: %w", issueID, err)
	}

	fingerprint := ""
	if issue, showErr := bc.Show(ctx, issueID); showErr == nil {
		fingerprint = beadsbridge.FingerprintIssue(issue)
	}

	taskMetadata := copyMetadata(metadata)
	taskMetadata["beads_issue_id"] = issueID
	taskMetadata["beads_bridge"] = "true"
	taskMetadata["ingress"] = "beads"

	fields := map[string]any{
		"title":       req.Title,
		"description": req.Description,
		"status":      string(types.StatusReady),
		"labels":      issueLabels,
		"parent_id":   req.ParentTaskID,
		"metadata":    taskMetadata,
	}
	if req.Priority > 0 {
		fields["priority"] = req.Priority
	}

	if _, getErr := e.dag.GetTask(ctx, issueID); getErr == nil {
		if err := e.dag.UpdateTask(ctx, issueID, fields); err != nil {
			return "", fmt.Errorf("update task %s from beads submit: %w", issueID, err)
		}
	} else if dag.IsNoRows(getErr) {
		task := dag.Task{
			ID:          issueID,
			Title:       req.Title,
			Description: req.Description,
			Status:      string(types.StatusReady),
			Project:     req.Project,
			Priority:    req.Priority,
			Labels:      issueLabels,
			ParentID:    req.ParentTaskID,
			Metadata:    taskMetadata,
		}
		if _, err := e.dag.CreateTask(ctx, task); err != nil {
			return "", fmt.Errorf("create task %s from beads submit: %w", issueID, err)
		}
	} else {
		return "", fmt.Errorf("get task %s for beads submit: %w", issueID, getErr)
	}

	if err := bridgeDAG.UpsertBeadsMapping(ctx, req.Project, issueID, issueID, fingerprint); err != nil {
		return "", fmt.Errorf("persist beads mapping for %s: %w", issueID, err)
	}

	e.logger.Info("Jarvis submitted task via beads",
		"task_id", issueID,
		"issue_id", issueID,
		"project", req.Project,
		"title", req.Title,
	)
	return issueID, nil
}

func copyMetadata(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
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
	tasks, err := e.dag.ListTasks(ctx, project, string(types.StatusReady), string(types.StatusRunning))
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

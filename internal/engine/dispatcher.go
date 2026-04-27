package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beadsbridge"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/perf"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// WorkflowDescriber is the subset of the Temporal client needed to check
// whether a workflow execution is still alive. Defined as an interface so
// tests can provide a mock without a real Temporal server.
type WorkflowDescriber interface {
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
}

// DispatcherWorkflow scans the DAG for ready tasks and spawns AgentWorkflow
// children. Designed to run on a Temporal Schedule (every tick_interval).
func DispatcherWorkflow(ctx workflow.Context, _ struct{}) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("DispatcherWorkflow tick")

	// Scan activities can include network I/O (beads bridge + git fetch), so
	// give them a wider timeout budget to avoid false StartToClose timeouts.
	scanOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	// Small write activities should fail fast.
	writeOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	var da *DispatchActivities
	scanCtx := workflow.WithActivityOptions(ctx, scanOpts)

	// === ZOMBIE RUNNING TASK RECOVERY ===
	// Reset tasks stuck in "running" whose agent workflow is dead.
	// Non-fatal — recovered tasks become candidates in the same tick.
	var recovered int
	if err := workflow.ExecuteActivity(scanCtx, da.ScanZombieRunningActivity).Get(ctx, &recovered); err != nil {
		logger.Error("Zombie running scan failed", "error", err)
	} else if recovered > 0 {
		logger.Info("Recovered zombie running tasks", "count", recovered)
	}

	var candidates []DispatchCandidate
	if err := workflow.ExecuteActivity(scanCtx, da.ScanCandidatesActivity).Get(ctx, &candidates); err != nil {
		logger.Error("Scan failed", "error", err)
		return fmt.Errorf("scan: %w", err)
	}

	if len(candidates) == 0 {
		logger.Info("No approved tasks")
	} else {
		logger.Info("Found candidates", "count", len(candidates))

		// Spawn AgentWorkflow for each candidate
		for _, c := range candidates {
			// Mark task as "running" BEFORE spawning child to prevent double-dispatch
			markCtx := workflow.WithActivityOptions(ctx, writeOpts)
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

			req := TaskRequest{
				TaskID:          c.TaskID,
				Project:         c.Project,
				Prompt:          c.Prompt,
				WorkDir:         c.WorkDir,
				Agent:           c.Agent,
				Model:           c.Model,
				Tier:            c.Tier,
				ParentID:        c.ParentID,
				ExecTimeout:     c.ExecTimeout,
				ShortTimeout:    c.ShortTimeout,
				ReviewTimeout:   c.ReviewTimeout,
				MaxReviewRounds: c.MaxReviewRounds,
				Metadata:        c.Metadata,
			}

			// Wait for child workflow to actually start — without this,
			// the parent completes before the server creates the child
			childFuture := workflow.ExecuteChildWorkflow(childCtx, AgentWorkflow, req)
			var childExecution workflow.Execution
			if err := childFuture.GetChildWorkflowExecution().Get(ctx, &childExecution); err != nil {
				logger.Error("Failed to start agent workflow", "TaskID", c.TaskID, "error", err)
				if isWorkflowAlreadyStartedError(err) {
					logger.Info("Agent workflow already started elsewhere; leaving task running", "TaskID", c.TaskID)
					continue
				}
				resetCtx := workflow.WithActivityOptions(ctx, writeOpts)
				if resetErr := workflow.ExecuteActivity(resetCtx, da.MarkTaskReadyActivity, c.TaskID).Get(ctx, nil); resetErr != nil {
					logger.Error("Failed to reset task after start failure", "TaskID", c.TaskID, "error", resetErr)
				}
				continue
			}
			startCtx := workflow.WithActivityOptions(ctx, writeOpts)
			if err := workflow.ExecuteActivity(startCtx, da.RecordDispatchStartActivity, c.TaskID, childExecution.ID).Get(ctx, nil); err != nil {
				logger.Warn("Failed to enqueue start projection event", "TaskID", c.TaskID, "error", err)
			}
			logger.Info("Dispatched agent", "TaskID", c.TaskID, "Agent", c.Agent, "Tier", c.Tier, "ChildWorkflowID", childExecution.ID)
		}
	}

	// === ORPHANED REVIEW RECOVERY ===
	// Scan for needs_review tasks with live PRs but no running workflow.
	var orphans []ReviewRequest
	if err := workflow.ExecuteActivity(scanCtx, da.ScanOrphanedReviewsActivity).Get(ctx, &orphans); err != nil {
		logger.Error("Orphan review scan failed", "error", err)
		// Non-fatal — continue normally.
	} else if len(orphans) > 0 {
		logger.Info("Found orphaned reviews to resume", "count", len(orphans))
		for _, o := range orphans {
			markCtx := workflow.WithActivityOptions(ctx, writeOpts)
			if err := workflow.ExecuteActivity(markCtx, da.MarkTaskRunningActivity, o.TaskID).Get(ctx, nil); err != nil {
				logger.Error("Failed to mark orphaned task running, skipping", "TaskID", o.TaskID, "error", err)
				continue
			}

			childOpts := workflow.ChildWorkflowOptions{
				WorkflowID:               fmt.Sprintf("chum-review-%s", o.TaskID),
				WorkflowExecutionTimeout: 1 * time.Hour,
				ParentClosePolicy:        enums.PARENT_CLOSE_POLICY_ABANDON,
			}
			childCtx := workflow.WithChildOptions(ctx, childOpts)

			childFuture := workflow.ExecuteChildWorkflow(childCtx, ReviewWorkflow, o)
			var childExecution workflow.Execution
			if err := childFuture.GetChildWorkflowExecution().Get(ctx, &childExecution); err != nil {
				logger.Error("Failed to start review workflow", "TaskID", o.TaskID, "PR", o.PRNumber, "error", err)
				if isWorkflowAlreadyStartedError(err) {
					logger.Info("Review workflow already started elsewhere; leaving task running", "TaskID", o.TaskID, "PR", o.PRNumber)
					continue
				}
				resetCtx := workflow.WithActivityOptions(ctx, writeOpts)
				if resetErr := workflow.ExecuteActivity(resetCtx, da.MarkTaskNeedsReviewActivity, o.TaskID).Get(ctx, nil); resetErr != nil {
					logger.Error("Failed to reset orphan review task after start failure", "TaskID", o.TaskID, "error", resetErr)
				}
				continue
			}
			logger.Info("Dispatched review recovery", "TaskID", o.TaskID, "PR", o.PRNumber, "ChildWorkflowID", childExecution.ID)
		}
	}

	// === WORKTREE CLEANUP ===
	// Remove worktree directories for tasks that have reached terminal status.
	var wtCleaned int
	if err := workflow.ExecuteActivity(scanCtx, da.CleanupOrphanedWorktreesActivity).Get(ctx, &wtCleaned); err != nil {
		logger.Error("Worktree cleanup failed", "error", err)
	} else if wtCleaned > 0 {
		logger.Info("Cleaned orphaned worktrees", "count", wtCleaned)
	}

	return nil
}

// DispatchCandidate is a ready task that should be dispatched.
type DispatchCandidate struct {
	TaskID          string
	Project         string
	Prompt          string
	WorkDir         string
	Agent           string
	Model           string
	Tier            string
	ParentID        string
	ExecTimeout     time.Duration
	ShortTimeout    time.Duration
	ReviewTimeout   time.Duration
	MaxReviewRounds int
	Metadata        map[string]string
}

// DispatchActivities holds dependencies for dispatch-related activities.
type DispatchActivities struct {
	DAG          dag.TaskStore
	Config       *config.Config
	Logger       *slog.Logger
	Perf         *perf.Tracker // performance-based provider selection (nil = config-only)
	BeadsClients map[string]beads.Store

	Temporal WorkflowDescriber // for checking workflow liveness (nil = skip zombie scan)

	reconcileMu   sync.Mutex
	lastReconcile map[string]time.Time
}

// MarkTaskRunningActivity marks a task as "running" in the DAG.
// Called before spawning the child workflow to prevent double-dispatch.
func (da *DispatchActivities) MarkTaskRunningActivity(ctx context.Context, taskID string) error {
	return da.DAG.UpdateTaskStatus(ctx, taskID, string(types.StatusRunning))
}

// MarkTaskReadyActivity marks a task as "ready" when dispatch startup fails.
func (da *DispatchActivities) MarkTaskReadyActivity(ctx context.Context, taskID string) error {
	return da.DAG.UpdateTaskStatus(ctx, taskID, string(types.StatusReady))
}

// MarkTaskNeedsReviewActivity marks a task as "needs_review" when orphan review
// startup fails and no review workflow was launched.
func (da *DispatchActivities) MarkTaskNeedsReviewActivity(ctx context.Context, taskID string) error {
	return da.DAG.UpdateTaskStatus(ctx, taskID, string(types.StatusNeedsReview))
}

// RecordDispatchStartActivity emits an idempotent start projection event.
func (da *DispatchActivities) RecordDispatchStartActivity(ctx context.Context, taskID, workflowID string) error {
	if da.Config == nil || !da.Config.BeadsBridge.Enabled || da.Config.BeadsBridge.DryRun {
		return nil
	}
	bridgeDAG, ok := da.DAG.(*dag.DAG)
	if !ok {
		return nil
	}
	task, err := da.DAG.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load task for start projection: %w", err)
	}
	mapping, err := bridgeDAG.GetBeadsMappingByTask(ctx, task.Project, taskID)
	if err != nil {
		if dag.IsNoRows(err) {
			return nil
		}
		return fmt.Errorf("resolve beads mapping for task %s: %w", taskID, err)
	}
	return beadsbridge.EnqueueTaskStarted(ctx, bridgeDAG, task.Project, mapping.IssueID, taskID, workflowID)
}

// ScanCandidatesActivity discovers ready tasks across all enabled projects.
func (da *DispatchActivities) ScanCandidatesActivity(ctx context.Context) ([]DispatchCandidate, error) {
	paused, err := da.globalPauseEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("check global pause: %w", err)
	}
	if paused {
		da.Logger.Info("Global pause active, skipping candidate scan")
		return []DispatchCandidate{}, nil
	}

	type readyProject struct {
		name   string
		config config.Project
		ready  []dag.Task
	}
	var readyProjects []readyProject

	for projectName, project := range da.Config.Projects {
		if !project.Enabled {
			continue
		}
		tasks, err := da.scanProjectReadyTasks(ctx, projectName)
		if err != nil {
			return nil, err
		}
		if len(tasks) == 0 {
			continue
		}
		readyProjects = append(readyProjects, readyProject{
			name:   projectName,
			config: project,
			ready:  tasks,
		})
	}

	var pullWG sync.WaitGroup
	for _, rp := range readyProjects {
		rp := rp
		pullWG.Add(1)
		go func() {
			defer pullWG.Done()
			// Pull latest default branch only when this project has dispatch-ready work.
			pullMaster(ctx, rp.config.Workspace, da.Logger)
		}()
	}
	pullWG.Wait()

	var candidates []DispatchCandidate
	for _, rp := range readyProjects {
		candidates = append(candidates, da.buildProjectCandidates(ctx, rp.name, rp.config, rp.ready)...)
	}
	return candidates, nil
}

func (da *DispatchActivities) scanProjectReadyTasks(ctx context.Context, projectName string) ([]dag.Task, error) {
	if da.Config.BeadsBridge.Enabled {
		bridgeDAG, ok := da.DAG.(*dag.DAG)
		if !ok {
			da.Logger.Warn("Beads bridge enabled but DAG store does not support bridge primitives")
		} else {
			bc := da.BeadsClients[projectName]
			if bc == nil {
				da.Logger.Warn("Beads bridge enabled but project has no beads client", "project", projectName)
			} else {
				scanner := &beadsbridge.Scanner{
					DAG:    bridgeDAG,
					Config: da.Config.BeadsBridge,
					Logger: da.Logger,
				}
				scanRes, err := scanner.ScanProject(ctx, projectName, bc)
				if err != nil {
					return nil, fmt.Errorf("beads bridge scan for %s: %w", projectName, err)
				}
				da.Logger.Info("Beads bridge scan complete",
					"project", projectName,
					"candidates", scanRes.Candidates,
					"gate_passed", scanRes.GatePassed,
					"admitted", scanRes.Admitted,
					"updated", scanRes.Updated,
					"deduped", scanRes.Deduped,
					"edges_projected", scanRes.EdgesProjected,
					"edges_pruned", scanRes.EdgesPruned,
					"edges_pending", scanRes.EdgesPending,
					"dry_run", scanRes.DryRun,
				)

				if !da.Config.BeadsBridge.DryRun {
					outbox := &beadsbridge.OutboxWorker{
						DAG:    bridgeDAG,
						Logger: da.Logger,
					}
					processed, outErr := outbox.ProcessProject(ctx, projectName, bc, 25)
					if outErr != nil {
						return nil, fmt.Errorf("beads bridge outbox for %s: %w", projectName, outErr)
					}
					if processed > 0 {
						da.Logger.Info("Beads bridge outbox delivery cycle complete",
							"project", projectName,
							"processed", processed,
						)
					}
				}

				if da.shouldRunReconcile(projectName) {
					report, recErr := beadsbridge.ReconcileProject(ctx, bridgeDAG, bc, projectName, false, nil)
					if recErr != nil {
						return nil, fmt.Errorf("beads bridge reconcile for %s: %w", projectName, recErr)
					}
					da.markReconcileRun(projectName)
					if len(report.Items) > 0 {
						da.Logger.Info("Beads bridge reconcile drift report",
							"project", projectName,
							"count", len(report.Items),
							"dry_run", report.DryRun,
						)
					}
				}
			}
		}
	}

	tasks, err := da.DAG.GetApprovedNodes(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("get approved nodes for %s: %w", projectName, err)
	}
	if len(tasks) == 0 {
		return nil, nil
	}

	// If a parent is approved but already has children, it has already been
	// decomposed at least once. Re-running it causes duplicate decomposition
	// attempts and churn.
	tasks = da.suppressRedundantParentDispatch(ctx, projectName, tasks)
	if len(tasks) == 0 {
		return nil, nil
	}
	if ingressPolicyRequiresBeads(da.Config.BeadsBridge.IngressPolicy) {
		tasks = da.filterBeadsMappedReadyTasks(ctx, projectName, tasks)
		if len(tasks) == 0 {
			return nil, nil
		}
	}

	// Cap per project
	max := da.Config.General.MaxConcurrent
	if len(tasks) > max {
		tasks = tasks[:max]
	}

	sampleIDs := make([]string, 0, len(tasks))
	for i, task := range tasks {
		if i >= 5 {
			break
		}
		sampleIDs = append(sampleIDs, task.ID)
	}
	da.Logger.Info("Dispatch-ready tasks discovered",
		"project", projectName,
		"count", len(tasks),
		"sample", strings.Join(sampleIDs, ","),
	)
	return tasks, nil
}

// suppressRedundantParentDispatch removes ready parent tasks that already have
// child tasks and marks them decomposed to prevent repeated decomposition loops.
func (da *DispatchActivities) suppressRedundantParentDispatch(ctx context.Context, projectName string, ready []dag.Task) []dag.Task {
	childrenByParent, err := da.DAG.CountChildrenByParent(ctx, projectName)
	if err != nil {
		da.Logger.Warn("Unable to count children for parent-child suppression",
			"project", projectName, "error", err)
		return ready
	}
	if len(childrenByParent) == 0 {
		return ready
	}

	filtered := make([]dag.Task, 0, len(ready))
	for _, t := range ready {
		// Only auto-suppress top-level tasks. Subtasks are valid execution units.
		if strings.TrimSpace(t.ParentID) == "" && childrenByParent[t.ID] > 0 {
			if err := da.DAG.UpdateTaskStatus(ctx, t.ID, string(types.StatusDecomposed)); err != nil {
				da.Logger.Warn("Failed to auto-mark ready parent decomposed",
					"project", projectName, "task", t.ID, "children", childrenByParent[t.ID], "error", err)
			} else {
				da.Logger.Info("Auto-marked ready parent decomposed (children already exist)",
					"project", projectName, "task", t.ID, "children", childrenByParent[t.ID])
			}
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// filterBeadsMappedReadyTasks enforces beads-first ingress at dispatch time.
// Ready DAG tasks without a resolvable beads mapping are skipped.
func (da *DispatchActivities) filterBeadsMappedReadyTasks(ctx context.Context, projectName string, ready []dag.Task) []dag.Task {
	if da.Config == nil || !da.Config.BeadsBridge.Enabled || !ingressPolicyRequiresBeads(da.Config.BeadsBridge.IngressPolicy) {
		return ready
	}
	bridgeDAG, ok := da.DAG.(*dag.DAG)
	if !ok {
		return ready
	}
	bc := da.BeadsClients[projectName]

	// Batch-fetch all mappings at once instead of N+1 queries.
	taskIDs := make([]string, len(ready))
	for i, t := range ready {
		taskIDs[i] = t.ID
	}
	mappings, err := bridgeDAG.GetBeadsMappingsByTasks(ctx, projectName, taskIDs)
	if err != nil {
		da.Logger.Warn("Batch beads mapping fetch failed, falling back to individual lookups",
			"project", projectName, "error", err)
		mappings = nil
	}

	filtered := make([]dag.Task, 0, len(ready))
	for _, t := range ready {
		// Try batch result first.
		if mappings != nil {
			if m, ok := mappings[t.ID]; ok && strings.TrimSpace(m.IssueID) != "" {
				filtered = append(filtered, t)
				continue
			}
		}

		// Individual mapping lookup — covers batch-query failures and
		// tasks missing from the batch result.
		if m, mErr := bridgeDAG.GetBeadsMappingByTask(ctx, projectName, t.ID); mErr == nil && strings.TrimSpace(m.IssueID) != "" {
			filtered = append(filtered, t)
			continue
		}

		// Legacy recovery: for old tasks where task ID equals beads issue ID,
		// bootstrap the mapping on demand and allow dispatch.
		if bc != nil {
			issue, showErr := bc.Show(ctx, t.ID)
			if showErr == nil && strings.TrimSpace(issue.ID) != "" {
				fingerprint := beadsbridge.FingerprintIssue(issue)
				if upErr := bridgeDAG.UpsertBeadsMapping(ctx, projectName, issue.ID, t.ID, fingerprint); upErr != nil {
					da.Logger.Warn("Skipping ready task: failed to backfill beads mapping",
						"project", projectName, "task", t.ID, "issue", issue.ID, "error", upErr)
					continue
				}
				filtered = append(filtered, t)
				continue
			}
		}

		// Auto-bootstrap: tasks created by the planner/decomposer without
		// beads entries get a synthetic mapping so they can still dispatch
		// under beads_only ingress policy.
		syntheticID := "synthetic/" + t.ID
		if upErr := bridgeDAG.UpsertBeadsMapping(ctx, projectName, syntheticID, t.ID, "auto-bootstrapped"); upErr != nil {
			da.Logger.Warn("Skipping ready task: failed to auto-bootstrap beads mapping",
				"project", projectName, "task", t.ID, "error", upErr)
			continue
		}
		da.Logger.Info("Auto-bootstrapped beads mapping for planner-created task",
			"project", projectName, "task", t.ID, "issue", syntheticID)
		filtered = append(filtered, t)
	}
	return filtered
}

func ingressPolicyRequiresBeads(policy string) bool {
	p := strings.ToLower(strings.TrimSpace(policy))
	return p != "" && p != "legacy"
}

func (da *DispatchActivities) buildProjectCandidates(ctx context.Context, projectName string, project config.Project, tasks []dag.Task) []DispatchCandidate {
	candidates := make([]DispatchCandidate, 0, len(tasks))
	for _, t := range tasks {
		// Pick provider: try perf-informed selection first, fall back to config.
		startTier := TierForEstimate(t.EstimateMinutes)
		agent, model, tier := da.pickProvider(ctx, startTier)
		if strings.TrimSpace(agent) == "" {
			da.Logger.Warn("No enabled provider available; skipping candidate",
				"project", projectName,
				"task_id", t.ID,
				"start_tier", startTier,
			)
			continue
		}

		prompt := t.Description
		if t.Acceptance != "" {
			prompt += "\n\nAcceptance Criteria:\n" + t.Acceptance
		}

		candidates = append(candidates, DispatchCandidate{
			TaskID:          t.ID,
			Project:         projectName,
			Prompt:          prompt,
			WorkDir:         project.Workspace,
			Agent:           agent,
			Model:           model,
			Tier:            tier,
			ParentID:        t.ParentID,
			ExecTimeout:     da.Config.General.ExecTimeout.Duration,
			ShortTimeout:    da.Config.General.ShortTimeout.Duration,
			ReviewTimeout:   da.Config.General.ReviewTimeout.Duration,
			MaxReviewRounds: da.Config.General.MaxReviewRounds,
			Metadata:        t.Metadata,
		})
	}
	return candidates
}

// pickProvider tries perf-informed UCT selection first, then falls back to config.
// Perf picks are validated against enabled providers — stale/disabled providers are ignored.
func (da *DispatchActivities) pickProvider(ctx context.Context, tier string) (cli, model, resolvedTier string) {
	if da.Perf != nil {
		logger := activity.GetLogger(ctx)
		p, err := da.Perf.Pick(ctx, tier)
		if err != nil {
			logger.Warn("Perf provider selection failed, using config", "tier", tier, "error", err)
		} else if p != nil && da.isProviderConfigured(p.Agent, p.Model) {
			logger.Info("Perf-informed provider selected", "agent", p.Agent, "model", p.Model, "tier", p.Tier)
			return p.Agent, p.Model, p.Tier
		}
	}
	return PickProvider(da.Config, tier)
}

func (da *DispatchActivities) shouldRunReconcile(project string) bool {
	if da.Config == nil || da.Config.BeadsBridge.ReconcileInterval.Duration <= 0 {
		return false
	}
	da.reconcileMu.Lock()
	defer da.reconcileMu.Unlock()
	if da.lastReconcile == nil {
		da.lastReconcile = make(map[string]time.Time)
	}
	last := da.lastReconcile[project]
	return last.IsZero() || time.Since(last) >= da.Config.BeadsBridge.ReconcileInterval.Duration
}

func (da *DispatchActivities) markReconcileRun(project string) {
	da.reconcileMu.Lock()
	defer da.reconcileMu.Unlock()
	if da.lastReconcile == nil {
		da.lastReconcile = make(map[string]time.Time)
	}
	da.lastReconcile[project] = time.Now()
}

// isProviderConfigured checks if an (agent, model) pair is enabled in the current config.
// This prevents perf from selecting stale models after config rotation.
func (da *DispatchActivities) isProviderConfigured(agent, model string) bool {
	for _, p := range da.Config.Providers {
		if !p.Enabled || p.CLI != agent {
			continue
		}
		// If perf recorded no model (legacy data), accept any enabled CLI match.
		// Otherwise require exact model match.
		if model == "" || p.Model == model {
			return true
		}
	}
	return false
}

// pullMaster keeps the workspace in sync with origin when it's safe to do so.
// It only fast-forwards when on the default branch with a clean worktree.
// Non-fatal — if it fails, we proceed with whatever we have.
func pullMaster(ctx context.Context, workDir string, logger *slog.Logger) {
	if _, err := runGitCommand(ctx, workDir, "fetch", "--prune", "origin"); err != nil {
		logger.Warn("Failed to fetch from origin", "WorkDir", workDir, "Error", err.Error())
		return
	}

	remoteHead, err := runGitCommand(ctx, workDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		logger.Debug("Skipping workspace fast-forward: origin default branch unavailable", "WorkDir", workDir)
		return
	}
	remoteHead = strings.TrimSpace(remoteHead) // e.g. origin/master
	defaultBranch := strings.TrimPrefix(remoteHead, "origin/")
	if defaultBranch == "" || defaultBranch == remoteHead {
		logger.Debug("Skipping workspace fast-forward: could not resolve default branch", "WorkDir", workDir, "RemoteHead", remoteHead)
		return
	}

	currentBranch, err := runGitCommand(ctx, workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		logger.Warn("Failed to detect current branch", "WorkDir", workDir, "Error", err.Error())
		return
	}
	currentBranch = strings.TrimSpace(currentBranch)
	if currentBranch == "HEAD" {
		logger.Debug("Skipping workspace fast-forward: detached HEAD", "WorkDir", workDir)
		return
	}
	if currentBranch != defaultBranch {
		logger.Debug("Skipping workspace fast-forward: non-default branch checked out", "WorkDir", workDir, "Branch", currentBranch, "DefaultBranch", defaultBranch)
		return
	}

	statusOut, err := runGitCommand(ctx, workDir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		logger.Warn("Failed to inspect worktree status", "WorkDir", workDir, "Error", err.Error())
		return
	}
	if strings.TrimSpace(statusOut) != "" {
		logger.Debug("Skipping workspace fast-forward: worktree has local changes", "WorkDir", workDir)
		return
	}

	divergenceOut, err := runGitCommand(ctx, workDir, "rev-list", "--left-right", "--count", "HEAD..."+remoteHead)
	if err != nil {
		logger.Warn("Failed to inspect branch divergence", "WorkDir", workDir, "Error", err.Error())
		return
	}
	ahead, behind, parseErr := parseAheadBehind(divergenceOut)
	if parseErr != nil {
		logger.Warn("Failed to parse branch divergence", "WorkDir", workDir, "Output", strings.TrimSpace(divergenceOut), "Error", parseErr.Error())
		return
	}
	if behind == 0 {
		return
	}
	if ahead > 0 {
		logger.Debug("Skipping workspace fast-forward: local branch is diverged/ahead", "WorkDir", workDir, "Ahead", ahead, "Behind", behind)
		return
	}

	if _, err := runGitCommand(ctx, workDir, "merge", "--ff-only", remoteHead); err != nil {
		logger.Warn("Failed to fast-forward workspace", "WorkDir", workDir, "Branch", currentBranch, "RemoteHead", remoteHead, "Error", err.Error())
		return
	}
	logger.Info("Pulled latest from origin", "WorkDir", workDir)
}

func parseAheadBehind(out string) (ahead, behind int, err error) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("expected two counts, got %q", out)
	}
	ahead, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse ahead count: %w", err)
	}
	behind, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse behind count: %w", err)
	}
	return ahead, behind, nil
}

func runGitCommand(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

// ScanZombieRunningActivity finds tasks stuck in "running" whose agent workflow
// is no longer alive in Temporal. In normal mode they are reset to "ready";
// while globally paused they are moved to "needs_review" to avoid re-dispatch.
func (da *DispatchActivities) ScanZombieRunningActivity(ctx context.Context) (int, error) {
	if da.Temporal == nil {
		return 0, nil
	}
	paused, err := da.globalPauseEnabled(ctx)
	if err != nil {
		return 0, fmt.Errorf("check global pause: %w", err)
	}
	logger := da.Logger
	var recovered int

	for projectName, project := range da.Config.Projects {
		if !project.Enabled {
			continue
		}

		tasks, err := da.DAG.ListTasks(ctx, projectName, string(types.StatusRunning))
		if err != nil {
			logger.Error("Failed to list running tasks", "project", projectName, "error", err)
			continue
		}

		for _, t := range tasks {
			if err := ctx.Err(); err != nil {
				return recovered, err
			}

			agentWorkflowID := fmt.Sprintf("chum-agent-%s", t.ID)
			reviewWorkflowID := fmt.Sprintf("chum-review-%s", t.ID)

			agentDesc, agentErr := da.Temporal.DescribeWorkflowExecution(ctx, agentWorkflowID, "")
			// Most running tasks are AgentWorkflow-owned. Short-circuit to avoid
			// an unnecessary second Describe call on the hot path.
			if workflowExecutionActive(agentDesc) {
				continue
			}

			reviewDesc, reviewErr := da.Temporal.DescribeWorkflowExecution(ctx, reviewWorkflowID, "")
			if workflowExecutionActive(reviewDesc) {
				continue
			}
			reason := zombieRecoveryReason(agentDesc, agentErr, reviewDesc, reviewErr)
			if da.handleZombieRecovery(ctx, t.ID, projectName, reason, paused) {
				recovered++
			}
		}
	}

	return recovered, nil
}

func workflowExecutionActive(desc *workflowservice.DescribeWorkflowExecutionResponse) bool {
	if desc == nil || desc.WorkflowExecutionInfo == nil {
		return false
	}
	switch desc.WorkflowExecutionInfo.Status {
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		enums.WORKFLOW_EXECUTION_STATUS_FAILED,
		enums.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		enums.WORKFLOW_EXECUTION_STATUS_CANCELED,
		enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return false
	default:
		return true
	}
}

func zombieRecoveryReason(
	agentDesc *workflowservice.DescribeWorkflowExecutionResponse,
	agentErr error,
	reviewDesc *workflowservice.DescribeWorkflowExecutionResponse,
	reviewErr error,
) string {
	switch {
	case reviewErr == nil && reviewDesc != nil && reviewDesc.WorkflowExecutionInfo != nil:
		return "review workflow " + strings.ToLower(reviewDesc.WorkflowExecutionInfo.Status.String())
	case agentErr == nil && agentDesc != nil && agentDesc.WorkflowExecutionInfo != nil:
		return "agent workflow " + strings.ToLower(agentDesc.WorkflowExecutionInfo.Status.String())
	case agentErr != nil && reviewErr != nil:
		return "agent/review workflows not found"
	default:
		return "workflow not found"
	}
}

func (da *DispatchActivities) globalPauseEnabled(ctx context.Context) (bool, error) {
	paused, isSet, err := da.DAG.IsGlobalPauseSet(ctx)
	if err != nil {
		return false, err
	}
	if isSet {
		return paused, nil // DB value overrides config
	}
	return da.Config.General.Paused, nil // no DB row: fall back to config
}

func (da *DispatchActivities) handleZombieRecovery(ctx context.Context, taskID, projectName, reason string, paused bool) bool {
	target := types.StatusReady
	logMsg := "Zombie detected, resetting to ready"
	if paused {
		target = types.StatusNeedsReview
		logMsg = "Zombie detected while globally paused, moving to needs_review"
	}

	da.Logger.Info(logMsg, "task", taskID, "project", projectName, "reason", reason)
	if err := da.DAG.UpdateTaskStatus(ctx, taskID, string(target)); err != nil {
		da.Logger.Error("Failed to recover zombie task", "task", taskID, "error", err)
		return false
	}
	return true
}

// ScanOrphanedReviewsActivity finds tasks in "needs_review" whose error_log
// contains a non-zero pr_number. These are orphaned — their AgentWorkflow died
// after creating a PR. Returns ReviewRequest objects ready for ReviewWorkflow.
func (da *DispatchActivities) ScanOrphanedReviewsActivity(ctx context.Context) ([]ReviewRequest, error) {
	logger := activity.GetLogger(ctx)
	var orphans []ReviewRequest

	for projectName, project := range da.Config.Projects {
		if !project.Enabled {
			continue
		}

		tasks, err := da.DAG.ListTasks(ctx, projectName, "needs_review")
		if err != nil {
			logger.Error("Failed to list needs_review tasks", "project", projectName, "error", err)
			continue
		}

		for _, t := range tasks {
			if strings.TrimSpace(t.ErrorLog) == "" {
				continue
			}

			var detail CloseDetail
			if err := json.Unmarshal([]byte(t.ErrorLog), &detail); err != nil {
				logger.Warn("Failed to parse error_log", "task", t.ID, "error", err)
				continue
			}
			if detail.PRNumber <= 0 {
				continue
			}
			if merged, err := isPRMerged(ctx, project.Workspace, detail.PRNumber); err == nil && merged {
				detail.Reason = CloseCompleted
				detail.SubReason = "completed"
				detail, err := closeTask(ctx, da.DAG, t.ID, detail)
				if err != nil {
					logger.Warn("Failed to auto-complete merged PR task", "task", t.ID, "pr", detail.PRNumber, "error", err)
					continue
				}
				projectTaskToBeads(ctx, logger, da.DAG, da.Config, da.BeadsClients, t.ID, detail)
				logger.Info("Auto-completed stale needs_review task with merged PR", "task", t.ID, "pr", detail.PRNumber)
				continue
			} else if err != nil {
				logger.Warn("PR merged-state check failed; continuing with orphan scan", "task", t.ID, "pr", detail.PRNumber, "error", err)
			}
			if !orphanReviewRecoverable(detail) {
				continue
			}

			// Pick provider the same way the normal dispatcher does.
			startTier := TierForEstimate(t.EstimateMinutes)
			agent, model, _ := PickProvider(da.Config, startTier)

			prompt := t.Description
			if t.Acceptance != "" {
				prompt += "\n\nAcceptance Criteria:\n" + t.Acceptance
			}

			orphans = append(orphans, ReviewRequest{
				TaskID:          t.ID,
				Project:         projectName,
				WorkDir:         project.Workspace,
				PRNumber:        detail.PRNumber,
				Agent:           agent,
				Model:           model,
				Prompt:          prompt,
				ExecTimeout:     da.Config.General.ExecTimeout.Duration,
				ShortTimeout:    da.Config.General.ShortTimeout.Duration,
				ReviewTimeout:   da.Config.General.ReviewTimeout.Duration,
				MaxReviewRounds: da.Config.General.MaxReviewRounds,
				Metadata:        t.Metadata,
			})
		}
	}

	if len(orphans) > 0 {
		logger.Info("Found orphaned reviews", "count", len(orphans))
	}
	return orphans, nil
}

func orphanReviewRecoverable(detail CloseDetail) bool {
	if detail.PRNumber <= 0 {
		return false
	}

	reason := strings.ToLower(strings.TrimSpace(string(detail.Reason)))
	if reason != "" && reason != string(CloseNeedsReview) {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(detail.SubReason)) {
	case "", "no_reviewer_activity", "reviewer_error", "review_submit_failed", "pr_info_failed", "worktree_failed":
		return true
	default:
		return false
	}
}

// terminalStatuses are task statuses that indicate the task is done and its
// worktree can be cleaned up.
var terminalStatuses = []string{
	string(types.StatusCompleted),
	string(types.StatusDone),
	string(types.StatusFailed),
	string(types.StatusDecomposed),
	string(types.StatusDoDFailed),
	string(types.StatusStale),
}

// CleanupOrphanedWorktreesActivity removes worktree directories for tasks that
// have reached terminal status.
func (da *DispatchActivities) CleanupOrphanedWorktreesActivity(ctx context.Context) (int, error) {
	worktreeBase := filepath.Join(os.TempDir(), "chum-worktrees")
	entries, err := os.ReadDir(worktreeBase)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read worktree base: %w", err)
	}

	var cleaned int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskID := entry.Name()
		task, err := da.DAG.GetTask(ctx, taskID)
		if err != nil {
			// Task not found in DAG — orphaned directory, safe to remove.
			wtPath := filepath.Join(worktreeBase, taskID)
			if err := os.RemoveAll(wtPath); err != nil {
				da.Logger.Warn("Failed to clean orphaned worktree (unknown task)", "path", wtPath, "error", err)
			} else {
				da.Logger.Info("Cleaned orphaned worktree (unknown task)", "task", taskID, "path", wtPath)
				cleaned++
			}
			continue
		}
		terminal := false
		for _, s := range terminalStatuses {
			if task.Status == s {
				terminal = true
				break
			}
		}
		if !terminal {
			continue
		}
		wtPath := filepath.Join(worktreeBase, taskID)
		if err := os.RemoveAll(wtPath); err != nil {
			da.Logger.Warn("Failed to clean orphaned worktree", "path", wtPath, "error", err)
			continue
		}
		da.Logger.Info("Cleaned orphaned worktree", "task", taskID, "path", wtPath)
		cleaned++
	}
	return cleaned, nil
}

func isWorkflowAlreadyStartedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already started")
}

func isPRMerged(ctx context.Context, workDir string, prNumber int) (bool, error) {
	out, err := runCommand(ctx, workDir, "gh", "pr", "view", strconv.Itoa(prNumber), "--json", "state")
	if err != nil {
		return false, err
	}
	var payload ghPRState
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return false, fmt.Errorf("parse PR state JSON: %w", err)
	}
	return strings.EqualFold(strings.TrimSpace(payload.State), "MERGED"), nil
}

package jarvis

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
)

// suggestCache caches LLM suggestions to avoid redundant calls.
var suggestCache = struct {
	sync.RWMutex
	m map[string]string
}{m: make(map[string]string)}

// --- Dashboard API handlers (read-only) ---

func (a *API) handleDashboardProjects(w http.ResponseWriter, _ *http.Request) {
	projects := make([]string, 0, len(a.Engine.workDirs))
	for name := range a.Engine.workDirs {
		projects = append(projects, name)
	}
	sort.Strings(projects)
	a.jsonOK(w, map[string]any{"projects": projects})
}

func (a *API) handleDashboardGraph(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	tasks, err := a.DAG.ListTasks(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type graphNode struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		ParentID string `json:"parent_id"`
		Priority int    `json:"priority"`
		Type     string `json:"type"`
	}

	type graphEdge struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Source string `json:"source"`
	}

	nodes := make([]graphNode, 0, len(tasks))
	for _, t := range tasks {
		nodes = append(nodes, graphNode{
			ID:       t.ID,
			Title:    t.Title,
			Status:   t.Status,
			ParentID: t.ParentID,
			Priority: t.Priority,
			Type:     t.Type,
		})
	}

	rows, err := a.DAG.DB().QueryContext(r.Context(),
		`SELECT e.from_task, e.to_task, e.source
		 FROM task_edges e
		 WHERE e.from_task IN (SELECT id FROM tasks WHERE project = ?)
		    OR e.to_task IN (SELECT id FROM tasks WHERE project = ?)`,
		project, project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var edges []graphEdge
	for rows.Next() {
		var e graphEdge
		if err := rows.Scan(&e.From, &e.To, &e.Source); err != nil {
			a.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if edges == nil {
		edges = []graphEdge{}
	}

	a.jsonOK(w, map[string]any{"nodes": nodes, "edges": edges})
}

func (a *API) handleDashboardTasks(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var statuses []string
	if s := r.URL.Query().Get("status"); s != "" {
		statuses = strings.Split(s, ",")
	}

	tasks, err := a.DAG.ListTasks(r.Context(), project, statuses...)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []dag.Task{}
	}

	a.jsonOK(w, map[string]any{"tasks": tasks})
}

func (a *API) handleDashboardTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")

	task, err := a.DAG.GetTask(r.Context(), taskID)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	deps, _ := a.DAG.GetDependencies(r.Context(), taskID)
	if deps == nil {
		deps = []string{}
	}
	dependents, _ := a.DAG.GetDependents(r.Context(), taskID)
	if dependents == nil {
		dependents = []string{}
	}
	targets, _ := a.DAG.GetTaskTargets(r.Context(), taskID)
	if targets == nil {
		targets = []dag.TaskTarget{}
	}

	type decisionWithAlts struct {
		dag.Decision
		Alternatives []dag.Alternative `json:"alternatives"`
	}

	decisions, _ := a.DAG.ListDecisionsForTask(r.Context(), taskID)
	var decs []decisionWithAlts
	for _, d := range decisions {
		alts, _ := a.DAG.ListAlternatives(r.Context(), d.ID)
		if alts == nil {
			alts = []dag.Alternative{}
		}
		decs = append(decs, decisionWithAlts{Decision: d, Alternatives: alts})
	}
	if decs == nil {
		decs = []decisionWithAlts{}
	}

	// Include execution traces if store available.
	var traces []store.ExecutionTrace
	if a.Store != nil {
		traces, _ = a.Store.ListExecutionTraces(taskID)
	}
	if traces == nil {
		traces = []store.ExecutionTrace{}
	}

	// Include lessons if store available.
	var lessons []store.StoredLesson
	if a.Store != nil {
		lessons, _ = a.Store.GetLessonsByTask(taskID)
	}
	if lessons == nil {
		lessons = []store.StoredLesson{}
	}

	planning, sessions := a.loadPlanningSnapshots(r.Context(), taskID)

	a.jsonOK(w, map[string]any{
		"task":              task,
		"dependencies":      deps,
		"dependents":        dependents,
		"targets":           targets,
		"decisions":         decs,
		"planning":          planning,
		"planning_sessions": sessions,
		"traces":            traces,
		"lessons":           lessons,
	})
}

func (a *API) handleDashboardPlanning(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	if _, err := a.DAG.GetTask(r.Context(), taskID); err != nil {
		a.jsonError(w, "task not found", http.StatusNotFound)
		return
	}

	planning, sessions := a.loadPlanningSnapshots(r.Context(), taskID)
	a.jsonOK(w, map[string]any{
		"task_id":  taskID,
		"planning": planning,
		"sessions": sessions,
	})
}

func (a *API) loadPlanningSnapshots(ctx context.Context, taskID string) (any, []dag.PlanningSnapshot) {
	latest, err := a.DAG.GetLatestPlanningSnapshotForTask(ctx, taskID)
	if err != nil {
		latest = dag.PlanningSnapshot{}
	}

	sessions, err := a.DAG.ListPlanningSnapshotsForTask(ctx, taskID)
	if err != nil || sessions == nil {
		sessions = []dag.PlanningSnapshot{}
	}

	if latest.SessionID == "" {
		return nil, sessions
	}
	return latest, sessions
}

func (a *API) handleDashboardStats(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	rows, err := a.DAG.DB().QueryContext(r.Context(),
		`SELECT status, COUNT(*) as cnt,
		        COALESCE(SUM(estimate_minutes), 0) as est,
		        COALESCE(SUM(actual_duration_sec), 0) as actual,
		        COALESCE(AVG(iterations_used), 0) as avg_iter
		 FROM tasks WHERE project = ?
		 GROUP BY status`, project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	byStatus := make(map[string]int)
	var total, totalEst, totalActual int
	var totalIter float64

	for rows.Next() {
		var status string
		var cnt, est, actual int
		var ai float64
		if err := rows.Scan(&status, &cnt, &est, &actual, &ai); err != nil {
			a.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		byStatus[status] = cnt
		total += cnt
		totalEst += est
		totalActual += actual
		totalIter += ai * float64(cnt)
	}
	avgIter := 0.0
	if total > 0 {
		avgIter = totalIter / float64(total)
	}

	a.jsonOK(w, map[string]any{
		"total":                  total,
		"by_status":              byStatus,
		"total_estimate_minutes": totalEst,
		"total_actual_seconds":   totalActual,
		"avg_iterations":         avgIter,
	})
}

func (a *API) handleDashboardTimeline(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	tasks, err := a.DAG.ListTasks(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []dag.Task{}
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})

	a.jsonOK(w, map[string]any{"tasks": tasks})
}

// handleDashboardTree returns a unified decomposition tree for a project.
// Merges parent→child relationships, decisions, and dependency edges into
// a navigable tree structure showing how goals decomposed into tasks.
func (a *API) handleDashboardTree(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	tasks, err := a.DAG.ListTasks(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type treeDecision struct {
		ID           string            `json:"id"`
		Title        string            `json:"title"`
		Outcome      string            `json:"outcome"`
		Alternatives []dag.Alternative `json:"alternatives"`
	}

	type treeNode struct {
		ID              string         `json:"id"`
		Title           string         `json:"title"`
		Status          string         `json:"status"`
		Type            string         `json:"type"`
		ParentID        string         `json:"parent_id"`
		EstimateMinutes int            `json:"estimate_minutes"`
		ActualDurationS int            `json:"actual_duration_sec"`
		IterationsUsed  int            `json:"iterations_used"`
		ErrorLog        string         `json:"error_log,omitempty"`
		CreatedAt       time.Time      `json:"created_at"`
		UpdatedAt       time.Time      `json:"updated_at"`
		Children        []string       `json:"children"`
		Dependencies    []string       `json:"dependencies"`
		Dependents      []string       `json:"dependents"`
		Decisions       []treeDecision `json:"decisions"`
		HasTraces       bool           `json:"has_traces"`
	}

	// Build parent→children map
	childMap := map[string][]string{}
	for _, t := range tasks {
		if t.ParentID != "" {
			childMap[t.ParentID] = append(childMap[t.ParentID], t.ID)
		}
	}

	nodes := make([]treeNode, 0, len(tasks))
	for _, t := range tasks {
		deps, _ := a.DAG.GetDependencies(r.Context(), t.ID)
		if deps == nil {
			deps = []string{}
		}
		dependents, _ := a.DAG.GetDependents(r.Context(), t.ID)
		if dependents == nil {
			dependents = []string{}
		}
		children := childMap[t.ID]
		if children == nil {
			children = []string{}
		}

		// Get decisions for this task
		var decs []treeDecision
		decisions, _ := a.DAG.ListDecisionsForTask(r.Context(), t.ID)
		for _, d := range decisions {
			alts, _ := a.DAG.ListAlternatives(r.Context(), d.ID)
			if alts == nil {
				alts = []dag.Alternative{}
			}
			decs = append(decs, treeDecision{
				ID:           d.ID,
				Title:        d.Title,
				Outcome:      d.Outcome,
				Alternatives: alts,
			})
		}
		if decs == nil {
			decs = []treeDecision{}
		}

		// Check for traces
		hasTraces := false
		if a.Store != nil {
			traces, _ := a.Store.ListExecutionTraces(t.ID)
			hasTraces = len(traces) > 0
		}

		nodes = append(nodes, treeNode{
			ID:              t.ID,
			Title:           t.Title,
			Status:          t.Status,
			Type:            t.Type,
			ParentID:        t.ParentID,
			EstimateMinutes: t.EstimateMinutes,
			ActualDurationS: t.ActualDurationS,
			IterationsUsed:  t.IterationsUsed,
			ErrorLog:        t.ErrorLog,
			CreatedAt:       t.CreatedAt,
			UpdatedAt:       t.UpdatedAt,
			Children:        children,
			Dependencies:    deps,
			Dependents:      dependents,
			Decisions:       decs,
			HasTraces:       hasTraces,
		})
	}

	// Identify root nodes (no parent)
	var roots []string
	for _, t := range tasks {
		if t.ParentID == "" {
			roots = append(roots, t.ID)
		}
	}
	if roots == nil {
		roots = []string{}
	}

	a.jsonOK(w, map[string]any{"nodes": nodes, "roots": roots})
}

// handleDashboardOverview returns a "mission control" summary:
// what's running, what just finished, what needs attention.
func (a *API) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	tasks, err := a.DAG.ListTasks(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type summaryTask struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Status    string    `json:"status"`
		UpdatedAt time.Time `json:"updated_at"`
		ErrorLog  string    `json:"error_log,omitempty"`
	}

	var running, recent, attention []summaryTask
	now := time.Now()

	for _, t := range tasks {
		st := summaryTask{
			ID:        t.ID,
			Title:     t.Title,
			Status:    t.Status,
			UpdatedAt: t.UpdatedAt,
			ErrorLog:  t.ErrorLog,
		}

		switch t.Status {
		case "running":
			running = append(running, st)
		case "completed", "done":
			if now.Sub(t.UpdatedAt) < 24*time.Hour {
				recent = append(recent, st)
			}
		case "failed", "dod_failed", "needs_refinement", "needs_review", "rejected":
			attention = append(attention, st)
		}
	}

	if running == nil {
		running = []summaryTask{}
	}
	if recent == nil {
		recent = []summaryTask{}
	}
	if attention == nil {
		attention = []summaryTask{}
	}

	// Sort recent by updated_at descending
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].UpdatedAt.After(recent[j].UpdatedAt)
	})
	// Limit recent to 10
	if len(recent) > 10 {
		recent = recent[:10]
	}

	// Stats summary
	byStatus := make(map[string]int)
	for _, t := range tasks {
		byStatus[t.Status]++
	}

	a.jsonOK(w, map[string]any{
		"running":   running,
		"recent":    recent,
		"attention": attention,
		"total":     len(tasks),
		"by_status": byStatus,
	})
}

// handleDashboardOverviewGrouped returns tasks grouped by parent goal with computed progress.
// computeGoalDisplayStatus derives a display status from children states.
func computeGoalDisplayStatus(rawStatus string, children []dag.Task) string {
	if len(children) == 0 {
		return rawStatus
	}
	allTerminal := true
	anyRunning := false
	anyFailed := false
	for _, c := range children {
		switch c.Status {
		case "completed", "done":
			// terminal
		case "failed", "dod_failed", "rejected":
			anyFailed = true
		case "running":
			anyRunning = true
			allTerminal = false
		default:
			allTerminal = false
		}
	}
	if rawStatus == "rejected" && allTerminal {
		return "rejected"
	}
	if allTerminal && !anyFailed {
		return "completed"
	}
	if anyRunning {
		return "running"
	}
	if anyFailed {
		return "has_failures"
	}
	return "in_progress"
}

func (a *API) handleDashboardOverviewGrouped(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	tasks, err := a.DAG.ListTasks(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type childTask struct {
		ID              string    `json:"id"`
		Title           string    `json:"title"`
		Status          string    `json:"status"`
		Type            string    `json:"type,omitempty"`
		Labels          []string  `json:"labels"`
		EstimateMinutes int       `json:"estimate_minutes,omitempty"`
		ActualDurationS int       `json:"actual_duration_sec,omitempty"`
		UpdatedAt       time.Time `json:"updated_at"`
		ErrorLog        string    `json:"error_log,omitempty"`
		BlockedByNames  []string  `json:"blocked_by_names"`
	}

	type goalGroup struct {
		Task             childTask   `json:"task"`
		DisplayStatus    string      `json:"display_status"`
		SubtaskTotal     int         `json:"subtask_total"`
		SubtaskCompleted int         `json:"subtask_completed"`
		SubtaskFailed    int         `json:"subtask_failed"`
		SubtaskRunning   int         `json:"subtask_running"`
		Health           string      `json:"health"`
		Children         []childTask `json:"children"`
		TotalEstimateMin int         `json:"total_estimate_minutes"`
		TotalActualSec   int         `json:"total_actual_duration_sec"`
	}

	// Index tasks by ID and build parent→children map
	taskMap := make(map[string]dag.Task, len(tasks))
	childMap := make(map[string][]dag.Task)
	for _, t := range tasks {
		taskMap[t.ID] = t
		if t.ParentID != "" {
			childMap[t.ParentID] = append(childMap[t.ParentID], t)
		}
	}

	toChild := func(t dag.Task) childTask {
		deps, _ := a.DAG.GetDependencies(r.Context(), t.ID)
		var names []string
		for _, depID := range deps {
			if dep, ok := taskMap[depID]; ok {
				names = append(names, dep.Title)
			}
		}
		if names == nil {
			names = []string{}
		}
		labels := t.Labels
		if labels == nil {
			labels = []string{}
		}
		return childTask{
			ID:              t.ID,
			Title:           t.Title,
			Status:          t.Status,
			Type:            t.Type,
			Labels:          labels,
			EstimateMinutes: t.EstimateMinutes,
			ActualDurationS: t.ActualDurationS,
			UpdatedAt:       t.UpdatedAt,
			ErrorLog:        t.ErrorLog,
			BlockedByNames:  names,
		}
	}

	var goals []goalGroup
	var orphans []childTask
	goalIDs := make(map[string]bool)

	// Identify goals: tasks that have children
	for parentID := range childMap {
		goalIDs[parentID] = true
	}

	// Also treat tasks with type "goal" as goals
	for _, t := range tasks {
		if t.Type == "goal" {
			goalIDs[t.ID] = true
		}
	}

	// Track which tasks are children of goals
	childOfGoal := make(map[string]bool)

	for _, t := range tasks {
		if !goalIDs[t.ID] {
			continue
		}
		children := childMap[t.ID]
		var completed, failed, running int
		var totalEst, totalActual int
		var childTasks []childTask
		for _, c := range children {
			childOfGoal[c.ID] = true
			switch c.Status {
			case "completed", "done":
				completed++
			case "failed", "dod_failed", "rejected":
				failed++
			case "running":
				running++
			}
			totalEst += c.EstimateMinutes
			totalActual += c.ActualDurationS
			childTasks = append(childTasks, toChild(c))
		}
		if childTasks == nil {
			childTasks = []childTask{}
		}

		total := len(children)
		health := "healthy"
		if total > 0 {
			failPct := float64(failed) / float64(total)
			if failPct > 0.3 {
				health = "failing"
			} else if failed > 0 {
				health = "degraded"
			}
		}

		goals = append(goals, goalGroup{
			Task:             toChild(t),
			DisplayStatus:    computeGoalDisplayStatus(t.Status, children),
			SubtaskTotal:     total,
			SubtaskCompleted: completed,
			SubtaskFailed:    failed,
			SubtaskRunning:   running,
			Health:           health,
			Children:         childTasks,
			TotalEstimateMin: totalEst,
			TotalActualSec:   totalActual,
		})
	}

	// Sort goals: failing first, then degraded, then healthy
	healthOrder := map[string]int{"failing": 0, "degraded": 1, "healthy": 2}
	sort.Slice(goals, func(i, j int) bool {
		return healthOrder[goals[i].Health] < healthOrder[goals[j].Health]
	})

	// Orphans: tasks that are not goals and not children of goals
	for _, t := range tasks {
		if !goalIDs[t.ID] && !childOfGoal[t.ID] {
			orphans = append(orphans, toChild(t))
		}
	}
	if goals == nil {
		goals = []goalGroup{}
	}
	if orphans == nil {
		orphans = []childTask{}
	}

	// Velocity: completed in last 24h and 7d
	now := time.Now()
	var completed24h, completed7d int
	for _, t := range tasks {
		if t.Status == "completed" || t.Status == "done" {
			if now.Sub(t.UpdatedAt) < 24*time.Hour {
				completed24h++
			}
			if now.Sub(t.UpdatedAt) < 7*24*time.Hour {
				completed7d++
			}
		}
	}

	// by_status counts
	byStatus := make(map[string]int)
	for _, t := range tasks {
		byStatus[t.Status]++
	}

	a.jsonOK(w, map[string]any{
		"goals":   goals,
		"orphans": orphans,
		"velocity": map[string]int{
			"completed_24h": completed24h,
			"completed_7d":  completed7d,
		},
		"total":     len(tasks),
		"by_status": byStatus,
	})
}

// handleDashboardTaskPause sets a running task back to ready.
func (a *API) handleDashboardTaskPause(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	task, err := a.DAG.GetTask(r.Context(), taskID)
	if err != nil {
		a.jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	if task.Status != "running" {
		a.jsonError(w, "task is not running", http.StatusBadRequest)
		return
	}
	if err := a.DAG.UpdateTaskStatus(r.Context(), taskID, "ready"); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, map[string]string{"task_id": taskID, "status": "ready"})
}

// handleDashboardTaskKill terminates a running or ready task.
// For running tasks, signals the Temporal workflow to cancel before updating DB.
func (a *API) handleDashboardTaskKill(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	task, err := a.DAG.GetTask(r.Context(), taskID)
	if err != nil {
		a.jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	if task.Status != "running" && task.Status != "ready" {
		a.jsonError(w, "task is not running or ready", http.StatusBadRequest)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Reason == "" {
		body.Reason = "killed via dashboard"
	}
	// Signal the Temporal workflow to cancel if the task is running.
	if task.Status == "running" && a.Engine != nil && a.Engine.temporal != nil {
		wfID := fmt.Sprintf("chum-agent-%s", taskID)
		_ = a.Engine.temporal.SignalWorkflow(r.Context(), wfID, "", "plan-cancel", body.Reason)
	}
	if err := a.DAG.UpdateTask(r.Context(), taskID, map[string]any{
		"status":    "failed",
		"error_log": body.Reason,
	}); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, map[string]string{"task_id": taskID, "status": "failed"})
}

// handleDashboardTaskRetry resets a failed task back to ready and triggers dispatch.
func (a *API) handleDashboardTaskRetry(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	task, err := a.DAG.GetTask(r.Context(), taskID)
	if err != nil {
		a.jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	terminal := map[string]bool{"failed": true, "dod_failed": true, "rejected": true, "needs_refinement": true}
	if !terminal[task.Status] {
		a.jsonError(w, "task is not in a retryable state", http.StatusBadRequest)
		return
	}
	if err := a.DAG.UpdateTask(r.Context(), taskID, map[string]any{
		"status":    "ready",
		"error_log": "",
	}); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a.Engine != nil {
		_ = a.Engine.TriggerDispatch(r.Context())
	}
	a.jsonOK(w, map[string]string{"task_id": taskID, "status": "ready"})
}

// handleDashboardTaskDecompose marks a task for decomposition.
func (a *API) handleDashboardTaskDecompose(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	if _, err := a.DAG.GetTask(r.Context(), taskID); err != nil {
		a.jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	if err := a.DAG.UpdateTaskStatus(r.Context(), taskID, "needs_refinement"); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a.Engine != nil {
		_ = a.Engine.TriggerDispatch(r.Context())
	}
	a.jsonOK(w, map[string]string{"task_id": taskID, "status": "needs_refinement"})
}

// handleDashboardTraces returns execution traces and graph events for a task.
func (a *API) handleDashboardTraces(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")

	traces, err := a.Store.ListExecutionTraces(taskID)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if traces == nil {
		traces = []store.ExecutionTrace{}
	}

	// For each trace, try to get graph events by session ID (task_id is used as session)
	type traceWithEvents struct {
		store.ExecutionTrace
		Events []*store.GraphTraceEvent `json:"events"`
	}

	// Fetch events once — they're keyed by taskID, not per-trace.
	allEvents, _ := a.Store.GetSessionTraceEvents(r.Context(), taskID)
	if allEvents == nil {
		allEvents = []*store.GraphTraceEvent{}
	}

	var results []traceWithEvents
	for _, t := range traces {
		results = append(results, traceWithEvents{
			ExecutionTrace: t,
			Events:         allEvents,
		})
	}
	if results == nil {
		results = []traceWithEvents{}
	}

	a.jsonOK(w, map[string]any{"traces": results})
}

// handleDashboardSuggest uses a cheap LLM call to suggest next actions for a stuck task.
func (a *API) handleDashboardSuggest(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")

	// Check cache first.
	suggestCache.RLock()
	if cached, ok := suggestCache.m[taskID]; ok {
		suggestCache.RUnlock()
		a.jsonOK(w, map[string]any{"task_id": taskID, "suggestion": cached})
		return
	}
	suggestCache.RUnlock()

	task, err := a.DAG.GetTask(r.Context(), taskID)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	// Build a triage prompt.
	prompt := fmt.Sprintf(`You are a triage assistant for an AI agent orchestration system called CHUM.
A task is stuck and needs human attention. Analyze the task and suggest 1-3 concrete next actions.

Task ID: %s
Title: %s
Status: %s
Description: %s
Error Log: %s

Respond with ONLY a brief, actionable suggestion (2-4 short sentences max). No preamble.
Focus on: what went wrong, what to do next, whether to retry/refine/abandon.`,
		task.ID,
		task.Title,
		task.Status,
		truncateStr(task.Description, 500),
		truncateStr(task.ErrorLog, 500),
	)

	result, err := a.LLM.Plan(r.Context(), "claude", "claude-haiku-4-5-20251001", ".", prompt)
	if err != nil {
		a.jsonError(w, fmt.Sprintf("LLM call failed: %v", err), http.StatusInternalServerError)
		return
	}

	suggestion := llm.UnwrapClaudeJSON(result.Output)
	if suggestion == "" {
		suggestion = result.Output
	}
	// Trim to reasonable length.
	if len(suggestion) > 1000 {
		suggestion = suggestion[:1000]
	}

	// Cache the result.
	suggestCache.Lock()
	suggestCache.m[taskID] = suggestion
	suggestCache.Unlock()

	a.jsonOK(w, map[string]any{"task_id": taskID, "suggestion": suggestion})
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// handleDashboardLessons returns recent lessons for a project.
func (a *API) handleDashboardLessons(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	lessons, err := a.Store.GetRecentLessons(project, 50)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if lessons == nil {
		lessons = []store.StoredLesson{}
	}

	a.jsonOK(w, map[string]any{"lessons": lessons})
}

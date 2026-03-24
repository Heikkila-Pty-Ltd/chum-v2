package jarvis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/metrics"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// suggestCache caches LLM suggestions with a TTL to avoid redundant calls.
var suggestCache = struct {
	sync.RWMutex
	m   map[string]suggestEntry
	ttl time.Duration
}{m: make(map[string]suggestEntry), ttl: 10 * time.Minute}

type suggestEntry struct {
	value   string
	created time.Time
}

// suggestLimiter enforces a minimum interval between LLM suggest calls.
var suggestLimiter = struct {
	sync.Mutex
	lastCall time.Time
	interval time.Duration
}{interval: 2 * time.Second}

// validParam checks that a path parameter contains only safe characters
// (alphanumeric, hyphens, underscores, dots, colons).
func validParam(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == ':') {
			return false
		}
	}
	return true
}

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

type planningStartRequest struct {
	Project string `json:"project"`
	GoalID  string `json:"goal_id"`
	Agent   string `json:"agent"`
}

func (a *API) handleDashboardPlanningStart(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil || a.Engine.temporal == nil {
		a.jsonError(w, "planning control unavailable", http.StatusServiceUnavailable)
		return
	}

	var req planningStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Project = strings.TrimSpace(req.Project)
	req.GoalID = strings.TrimSpace(req.GoalID)
	req.Agent = strings.TrimSpace(req.Agent)
	if req.Project == "" || req.GoalID == "" {
		a.jsonError(w, "project and goal_id are required", http.StatusBadRequest)
		return
	}

	task, err := a.DAG.GetTask(r.Context(), req.GoalID)
	if err != nil {
		a.jsonError(w, "goal task not found", http.StatusNotFound)
		return
	}
	if task.Project != "" && task.Project != req.Project {
		a.jsonError(w, "goal task project does not match request", http.StatusBadRequest)
		return
	}

	workDir := a.Engine.workDirs[req.Project]
	if workDir == "" {
		a.jsonError(w, "unknown project", http.StatusBadRequest)
		return
	}

	agent := req.Agent
	if agent == "" {
		agent = a.PlanningDefaultAgent
	}
	if agent == "" {
		agent = "claude"
	}

	workflowID := planningWorkflowID(req.Project, req.GoalID)
	sessionID := fmt.Sprintf("%s-%d", workflowID, time.Now().UnixNano())
	run, err := a.Engine.temporal.ExecuteWorkflow(r.Context(), client.StartWorkflowOptions{
		ID:                                       workflowID,
		TaskQueue:                                a.Engine.taskQueue,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}, planning.PlanningWorkflow, planning.PlanningRequest{
		GoalID:     req.GoalID,
		Project:    req.Project,
		WorkDir:    workDir,
		Agent:      agent,
		Source:     "dashboard-control",
		SessionID:  sessionID,
		WorkflowID: workflowID,
	}, a.PlanningCfg)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			latest := a.latestPlanningSnapshotForWorkflow(r.Context(), req.GoalID, workflowID)
			if latest.SessionID != "" {
				latest.WorkflowStatus, latest.WorkflowActive = a.describePlanningWorkflow(r.Context(), workflowID)
			}
			a.jsonOK(w, map[string]any{
				"session_id":  latest.SessionID,
				"workflow_id": workflowID,
				"run_id":      alreadyStarted.RunId,
				"started":     false,
				"planning":    latest,
			})
			return
		}
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.jsonOK(w, map[string]any{
		"session_id":  sessionID,
		"workflow_id": workflowID,
		"run_id":      run.GetRunID(),
		"started":     true,
	})
}

type planningSignalRequest struct {
	Action string `json:"action"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

func (a *API) handleDashboardPlanningSignal(w http.ResponseWriter, r *http.Request) {
	if a.Engine == nil || a.Engine.temporal == nil {
		a.jsonError(w, "planning control unavailable", http.StatusServiceUnavailable)
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if sessionID == "" {
		a.jsonError(w, "session_id required", http.StatusBadRequest)
		return
	}

	var req planningSignalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Value = strings.TrimSpace(req.Value)
	req.Reason = strings.TrimSpace(req.Reason)

	signalName, payload, err := translatePlanningSignal(req)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := a.Engine.temporal.SignalWorkflow(r.Context(), sessionID, "", signalName, payload); err != nil {
		a.jsonError(w, err.Error(), http.StatusBadGateway)
		return
	}

	a.jsonOK(w, map[string]any{
		"session_id": sessionID,
		"action":     req.Action,
		"status":     "ok",
	})
}

func (a *API) loadPlanningSnapshots(ctx context.Context, taskID string) (any, []dag.PlanningSnapshot) {
	latest, err := a.DAG.GetLatestPlanningSnapshotForTask(ctx, taskID)
	if err != nil {
		latest = dag.PlanningSnapshot{}
	}
	latest = a.decoratePlanningSnapshot(latest)
	if controlID := planningControlID(latest); controlID != "" {
		latest.WorkflowStatus, latest.WorkflowActive = a.describePlanningWorkflow(ctx, controlID)
	}

	sessions, err := a.DAG.ListPlanningSnapshotsForTask(ctx, taskID)
	if err != nil || sessions == nil {
		sessions = []dag.PlanningSnapshot{}
	}
	for i := range sessions {
		sessions[i] = a.decoratePlanningSnapshot(sessions[i])
	}

	if latest.SessionID == "" {
		return nil, sessions
	}
	return latest, sessions
}

func planningWorkflowID(project, goalID string) string {
	return fmt.Sprintf("planning-%s-%s", project, goalID)
}

func planningControlID(snapshot dag.PlanningSnapshot) string {
	if strings.TrimSpace(snapshot.WorkflowID) != "" {
		return strings.TrimSpace(snapshot.WorkflowID)
	}
	return strings.TrimSpace(snapshot.SessionID)
}

func (a *API) decoratePlanningSnapshot(snapshot dag.PlanningSnapshot) dag.PlanningSnapshot {
	if snapshot.SessionID == "" {
		return snapshot
	}
	if strings.TrimSpace(snapshot.WorkflowID) == "" && snapshot.Source == "dashboard-control" && snapshot.Project != "" && snapshot.GoalID != "" {
		snapshot.WorkflowID = planningWorkflowID(snapshot.Project, snapshot.GoalID)
	}
	if strings.TrimSpace(snapshot.WorkflowID) == "" {
		snapshot.WorkflowID = snapshot.SessionID
	}
	return snapshot
}

func (a *API) latestPlanningSnapshotForWorkflow(ctx context.Context, taskID, workflowID string) dag.PlanningSnapshot {
	latest, err := a.DAG.GetLatestPlanningSnapshotForTask(ctx, taskID)
	if err != nil {
		return dag.PlanningSnapshot{}
	}
	latest = a.decoratePlanningSnapshot(latest)
	if planningControlID(latest) == workflowID {
		return latest
	}
	return dag.PlanningSnapshot{}
}

func (a *API) describePlanningWorkflow(ctx context.Context, sessionID string) (string, bool) {
	if a.Engine == nil || a.Engine.temporal == nil || strings.TrimSpace(sessionID) == "" {
		return "", false
	}
	desc, err := a.Engine.temporal.DescribeWorkflowExecution(ctx, sessionID, "")
	if err != nil || desc == nil || desc.WorkflowExecutionInfo == nil {
		return "", false
	}
	status := strings.ToLower(desc.WorkflowExecutionInfo.Status.String())
	switch desc.WorkflowExecutionInfo.Status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return status, true
	default:
		return status, false
	}
}

func translatePlanningSignal(req planningSignalRequest) (string, string, error) {
	switch req.Action {
	case "select":
		if req.Value == "" {
			return "", "", fmt.Errorf("select requires value")
		}
		return planning.SignalNameSelect, req.Value, nil
	case "dig":
		if req.Value == "" {
			return "", "", fmt.Errorf("dig requires value")
		}
		if req.Reason != "" {
			return planning.SignalNameDig, req.Value + "|" + req.Reason, nil
		}
		return planning.SignalNameDig, req.Value, nil
	case "answer":
		if req.Value == "" {
			return "", "", fmt.Errorf("answer requires value")
		}
		return planning.SignalNameQuestion, req.Value, nil
	case "go":
		return planning.SignalNameGreenlight, "GO", nil
	case "approve":
		return planning.SignalNameApproveDecomp, "APPROVED", nil
	case "realign":
		return planning.SignalNameGreenlight, "REALIGN", nil
	case "stop":
		return planning.SignalNameCancel, req.Reason, nil
	default:
		return "", "", fmt.Errorf("unknown planning action %q", req.Action)
	}
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
	if !validParam(project) {
		a.jsonError(w, "invalid project name", http.StatusBadRequest)
		return
	}

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

	// Bulk-fetch edges, decisions, alternatives, and trace existence
	// to avoid N+1 queries per task.
	depsMap, dependentsMap, _ := a.DAG.GetProjectEdges(r.Context(), project)
	if depsMap == nil {
		depsMap = map[string][]string{}
	}
	if dependentsMap == nil {
		dependentsMap = map[string][]string{}
	}

	decisionsMap, _ := a.DAG.ListDecisionsForProject(r.Context(), project)
	if decisionsMap == nil {
		decisionsMap = map[string][]dag.Decision{}
	}

	altsMap, _ := a.DAG.ListAlternativesForProject(r.Context(), project)
	if altsMap == nil {
		altsMap = map[string][]dag.Alternative{}
	}

	traceSet := map[string]bool{}
	if a.Store != nil {
		taskIDs := make([]string, len(tasks))
		for i, t := range tasks {
			taskIDs[i] = t.ID
		}
		traceSet, _ = a.Store.TaskIDsWithTraces(taskIDs)
		if traceSet == nil {
			traceSet = map[string]bool{}
		}
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
		deps := depsMap[t.ID]
		if deps == nil {
			deps = []string{}
		}
		dependents := dependentsMap[t.ID]
		if dependents == nil {
			dependents = []string{}
		}
		children := childMap[t.ID]
		if children == nil {
			children = []string{}
		}

		var decs []treeDecision
		for _, d := range decisionsMap[t.ID] {
			alts := altsMap[d.ID]
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
			HasTraces:       traceSet[t.ID],
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
		case "failed", "dod_failed", "needs_refinement", "needs_review", "rejected", "quarantined", "budget_exceeded":
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
		case "failed", "dod_failed", "rejected", "quarantined", "budget_exceeded":
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
	if !validParam(project) {
		a.jsonError(w, "invalid project name", http.StatusBadRequest)
		return
	}

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

	// Bulk-fetch edges to avoid N+1 per-task dependency lookups.
	depsMap, _, _ := a.DAG.GetProjectEdges(r.Context(), project)
	if depsMap == nil {
		depsMap = map[string][]string{}
	}

	toChild := func(t dag.Task) childTask {
		var names []string
		for _, depID := range depsMap[t.ID] {
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
			case "failed", "dod_failed", "rejected", "quarantined", "budget_exceeded":
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
	retryable := map[string]bool{
		"failed": true, "dod_failed": true, "rejected": true, "needs_refinement": true,
		"quarantined": true, "budget_exceeded": true,
	}
	if !retryable[task.Status] {
		a.jsonError(w, "task is not in a retryable state", http.StatusBadRequest)
		return
	}
	updates := map[string]any{
		"status":    "ready",
		"error_log": "",
	}
	if task.Status == "quarantined" || task.Status == "budget_exceeded" {
		updates["attempt_count"] = 0
	}
	// Clear safety block for quarantined tasks.
	if task.Status == "quarantined" && a.Store != nil {
		_ = a.Store.RemoveBlock(taskID, "quarantine")
	}
	if err := a.DAG.UpdateTask(r.Context(), taskID, updates); err != nil {
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
	if !validParam(taskID) {
		a.jsonError(w, "invalid task ID", http.StatusBadRequest)
		return
	}

	// Check cache first (with TTL).
	suggestCache.RLock()
	if entry, ok := suggestCache.m[taskID]; ok && time.Since(entry.created) < suggestCache.ttl {
		suggestCache.RUnlock()
		a.jsonOK(w, map[string]any{"task_id": taskID, "suggestion": entry.value})
		return
	}
	suggestCache.RUnlock()

	// Rate limit LLM calls.
	suggestLimiter.Lock()
	if time.Since(suggestLimiter.lastCall) < suggestLimiter.interval {
		suggestLimiter.Unlock()
		a.jsonError(w, "rate limited, try again shortly", http.StatusTooManyRequests)
		return
	}
	suggestLimiter.lastCall = time.Now()
	suggestLimiter.Unlock()

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

	// Cache the result with TTL.
	suggestCache.Lock()
	suggestCache.m[taskID] = suggestEntry{value: suggestion, created: time.Now()}
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

// handleDashboardActivity returns a reverse-chronological activity feed
// for the Check page. Merges recent task state changes with recent lessons
// across all projects (or filtered by project via ?project= param).
func (a *API) handleDashboardActivity(w http.ResponseWriter, r *http.Request) {
	hoursStr := r.URL.Query().Get("hours")
	hours := 24
	if hoursStr != "" {
		if h, err := fmt.Sscanf(hoursStr, "%d", &hours); err != nil || h == 0 {
			hours = 24
		}
		if hours < 1 {
			hours = 1
		}
		if hours > 8760 {
			hours = 8760
		}
	}
	projectFilter := r.URL.Query().Get("project")

	type activityEvent struct {
		Type      string    `json:"type"`
		TaskID    string    `json:"task_id"`
		Title     string    `json:"title"`
		Project   string    `json:"project"`
		Status    string    `json:"status"`
		Outcome   string    `json:"outcome,omitempty"`
		Timestamp time.Time `json:"timestamp"`
		CostUSD   float64   `json:"cost_usd,omitempty"`
		Summary   string    `json:"summary,omitempty"`
		Category  string    `json:"category,omitempty"`
	}

	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
	var events []activityEvent

	// 1. Recent task changes from DAG db.
	taskQuery := `SELECT id, title, project, status, error_log, updated_at
		FROM tasks WHERE updated_at >= ?`
	args := []any{cutoff.UTC().Format("2006-01-02 15:04:05")}
	if projectFilter != "" {
		taskQuery += " AND project = ?"
		args = append(args, projectFilter)
	}
	taskQuery += " ORDER BY updated_at DESC LIMIT 200"

	rows, err := a.DAG.DB().QueryContext(r.Context(), taskQuery, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, title, project, status, errorLog string
			var updatedAt time.Time
			if err := rows.Scan(&id, &title, &project, &status, &errorLog, &updatedAt); err != nil {
				continue
			}
			ev := activityEvent{
				Type:      "task",
				TaskID:    id,
				Title:     title,
				Project:   project,
				Status:    status,
				Timestamp: updatedAt,
			}
			if errorLog != "" {
				ev.Outcome = truncateStr(errorLog, 200)
			}
			events = append(events, ev)
		}
	}

	// 2. Cost rollup per task from perf_runs (tracesDB).
	costByTask := map[string]float64{}
	if a.TracesDB != nil {
		costRows, err := a.TracesDB.QueryContext(r.Context(),
			`SELECT task_id, SUM(cost_usd) FROM perf_runs
			 WHERE task_id != '' AND created_at >= ?
			 GROUP BY task_id`,
			cutoff.UTC().Format("2006-01-02 15:04:05"))
		if err == nil {
			defer costRows.Close()
			for costRows.Next() {
				var tid string
				var cost float64
				if err := costRows.Scan(&tid, &cost); err == nil {
					costByTask[tid] = cost
				}
			}
		}
	}
	// Merge cost into task events.
	for i := range events {
		if c, ok := costByTask[events[i].TaskID]; ok {
			events[i].CostUSD = c
		}
	}

	// 3. Recent lessons from tracesDB.
	if a.TracesDB != nil {
		lessonQuery := `SELECT task_id, project, category, summary, created_at
			FROM lessons WHERE created_at >= ?`
		largs := []any{cutoff.UTC().Format("2006-01-02 15:04:05")}
		if projectFilter != "" {
			lessonQuery += " AND project = ?"
			largs = append(largs, projectFilter)
		}
		lessonQuery += " ORDER BY created_at DESC LIMIT 100"

		lrows, err := a.TracesDB.QueryContext(r.Context(), lessonQuery, largs...)
		if err == nil {
			defer lrows.Close()
			for lrows.Next() {
				var taskID, project, category, summary string
				var createdAt time.Time
				if err := lrows.Scan(&taskID, &project, &category, &summary, &createdAt); err != nil {
					continue
				}
				events = append(events, activityEvent{
					Type:      "lesson",
					TaskID:    taskID,
					Project:   project,
					Status:    "lesson",
					Category:  category,
					Summary:   summary,
					Timestamp: createdAt,
				})
			}
		}
	}

	// Sort merged events reverse-chronological.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})
	if len(events) > 300 {
		events = events[:300]
	}

	// Compute summary counts.
	var completedCount, failedCount, runningCount int
	var totalCost float64
	for _, ev := range events {
		if ev.Type != "task" {
			continue
		}
		switch ev.Status {
		case "completed", "done":
			completedCount++
		case "failed", "dod_failed", "rejected", "quarantined", "budget_exceeded":
			failedCount++
		case "running":
			runningCount++
		}
		totalCost += ev.CostUSD
	}

	if events == nil {
		events = []activityEvent{}
	}

	a.jsonOK(w, map[string]any{
		"events": events,
		"summary": map[string]any{
			"completed": completedCount,
			"failed":    failedCount,
			"running":   runningCount,
			"total_cost": totalCost,
		},
		"hours": hours,
	})
}

// handleDashboardProjectPause pauses all running/ready tasks in a project.
// Idempotent — re-calling when already paused returns affected: 0.
func (a *API) handleDashboardProjectPause(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("name")
	if !validParam(project) {
		a.jsonError(w, "invalid project name", http.StatusBadRequest)
		return
	}
	affected, err := a.DAG.PauseProjectTasks(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, map[string]any{
		"project":    project,
		"affected":   affected,
		"new_status": "paused",
	})
}

// handleDashboardProjectResume resumes all paused tasks in a project.
func (a *API) handleDashboardProjectResume(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("name")
	if !validParam(project) {
		a.jsonError(w, "invalid project name", http.StatusBadRequest)
		return
	}
	affected, err := a.DAG.ResumeProjectTasks(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a.Engine != nil {
		_ = a.Engine.TriggerDispatch(r.Context())
	}
	a.jsonOK(w, map[string]any{
		"project":    project,
		"affected":   affected,
		"new_status": "ready",
	})
}

// handleDashboardQueueReorder reorders task execution priorities.
// Body: {"task_ids": ["id1", "id2", ...]} in desired order.
func (a *API) handleDashboardQueueReorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(body.TaskIDs) == 0 {
		a.jsonError(w, "task_ids must not be empty", http.StatusBadRequest)
		return
	}
	if len(body.TaskIDs) > 1000 {
		a.jsonError(w, "too many task IDs (max 1000)", http.StatusBadRequest)
		return
	}
	// Validate no duplicates.
	seen := make(map[string]bool, len(body.TaskIDs))
	for _, id := range body.TaskIDs {
		if !validParam(id) {
			a.jsonError(w, fmt.Sprintf("invalid task ID: %s", id), http.StatusBadRequest)
			return
		}
		if seen[id] {
			a.jsonError(w, fmt.Sprintf("duplicate task ID: %s", id), http.StatusBadRequest)
			return
		}
		seen[id] = true
	}
	if err := a.DAG.ReorderTaskPriorities(r.Context(), body.TaskIDs); err != nil {
		a.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if a.Engine != nil {
		_ = a.Engine.TriggerDispatch(r.Context())
	}
	a.jsonOK(w, map[string]any{
		"reordered": len(body.TaskIDs),
	})
}

// handleDashboardLearningTrends returns daily aggregated perf metrics for the last 30 days.
func (a *API) handleDashboardLearningTrends(w http.ResponseWriter, r *http.Request) {
	if a.TracesDB == nil {
		a.jsonError(w, "traces database unavailable", http.StatusServiceUnavailable)
		return
	}

	rows, err := a.TracesDB.QueryContext(r.Context(),
		`SELECT date(created_at) as day,
		        COUNT(*) as total,
		        SUM(success) as successes,
		        CAST(SUM(success) AS REAL) / COUNT(*) as success_rate,
		        AVG(duration_s) as avg_duration,
		        SUM(cost_usd) as total_cost
		 FROM perf_runs
		 WHERE created_at >= date('now', '-30 days')
		 GROUP BY date(created_at)
		 ORDER BY day ASC`)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type trendDay struct {
		Day         string  `json:"day"`
		TotalRuns   int     `json:"total_runs"`
		Successes   int     `json:"successes"`
		SuccessRate float64 `json:"success_rate"`
		AvgDuration float64 `json:"avg_duration_s"`
		TotalCost   float64 `json:"total_cost_usd"`
	}

	dayMap := make(map[string]trendDay)
	for rows.Next() {
		var d trendDay
		if err := rows.Scan(&d.Day, &d.TotalRuns, &d.Successes, &d.SuccessRate, &d.AvgDuration, &d.TotalCost); err != nil {
			a.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		dayMap[d.Day] = d
	}

	// Gap-fill: ensure every day in the 30-day window is present.
	now := time.Now().UTC()
	var trends []trendDay
	for i := 29; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		if d, ok := dayMap[day]; ok {
			trends = append(trends, d)
		} else {
			trends = append(trends, trendDay{Day: day})
		}
	}

	a.jsonOK(w, map[string]any{
		"trends":      trends,
		"period_days": 30,
	})
}

// handleDashboardModelPerf returns per-model performance stats from perf_runs.
func (a *API) handleDashboardModelPerf(w http.ResponseWriter, r *http.Request) {
	if a.TracesDB == nil {
		a.jsonError(w, "traces database unavailable", http.StatusServiceUnavailable)
		return
	}

	rows, err := a.TracesDB.QueryContext(r.Context(),
		`SELECT agent, model, tier,
		        COUNT(*) as total_runs,
		        SUM(success) as successes,
		        CAST(SUM(success) AS REAL) / COUNT(*) as success_rate,
		        AVG(cost_usd) as avg_cost,
		        AVG(duration_s) as avg_duration
		 FROM perf_runs
		 GROUP BY agent, model, tier
		 ORDER BY total_runs DESC`)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type modelStat struct {
		Agent       string  `json:"agent"`
		Model       string  `json:"model"`
		Tier        string  `json:"tier"`
		TotalRuns   int     `json:"total_runs"`
		Successes   int     `json:"successes"`
		SuccessRate float64 `json:"success_rate"`
		AvgCost     float64 `json:"avg_cost_usd"`
		AvgDuration float64 `json:"avg_duration_s"`
	}

	var models []modelStat
	for rows.Next() {
		var m modelStat
		if err := rows.Scan(&m.Agent, &m.Model, &m.Tier, &m.TotalRuns, &m.Successes, &m.SuccessRate, &m.AvgCost, &m.AvgDuration); err != nil {
			a.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		models = append(models, m)
	}
	if models == nil {
		models = []modelStat{}
	}

	a.jsonOK(w, map[string]any{"models": models})
}

// handleDashboardHealthMetrics returns system-wide health metrics.
func (a *API) handleDashboardHealthMetrics(w http.ResponseWriter, r *http.Request) {
	report, err := metrics.CollectHealth(r.Context(), a.DAG.DB(), a.TracesDB)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, report)
}

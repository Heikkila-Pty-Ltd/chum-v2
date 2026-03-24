package jarvis

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/planning"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/plansession"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
)

// API exposes the Jarvis integration as HTTP endpoints.
// Mount on a local-only port — no auth needed for localhost.
type API struct {
	Engine *Engine
	DAG    *dag.DAG
	Store  *store.Store // trace/lesson/safety store; nil disables trace endpoints
	LLM    llm.Runner   // LLM runner for suggestions; nil disables suggest endpoint
	AST    *ast.Parser  // AST parser for codebase context; nil skips AST context
	Logger *slog.Logger
	WebDir string // directory for static dashboard files; empty disables serving

	IngressPolicy        string                          // legacy | beads_first | beads_only
	PlanningDefaultAgent string                          // default planner agent for dashboard starts
	PlanningCfg          planning.PlanningCeremonyConfig // dashboard planning ceremony config

	PlanSession *plansession.Manager // interactive planner session manager; nil disables session endpoints

	TracesDB *sql.DB // traces database handle for health metrics; nil disables health endpoint

}

// Handler returns an http.Handler with all Jarvis API routes.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/jarvis/submit", a.handleSubmit)
	mux.HandleFunc("GET /api/jarvis/status/{taskID}", a.handleStatus)
	mux.HandleFunc("GET /api/jarvis/pending/{project}", a.handlePending)
	mux.HandleFunc("GET /api/jarvis/health", a.handleHealth)

	// Dashboard API endpoints (read-only).
	if a.DAG != nil {
		mux.HandleFunc("GET /api/dashboard/projects", a.handleDashboardProjects)
		mux.HandleFunc("GET /api/dashboard/graph/{project}", a.handleDashboardGraph)
		mux.HandleFunc("GET /api/dashboard/tasks/{project}", a.handleDashboardTasks)
		mux.HandleFunc("GET /api/dashboard/task/{taskID}", a.handleDashboardTask)
		mux.HandleFunc("GET /api/dashboard/planning/{taskID}", a.handleDashboardPlanning)
		mux.HandleFunc("POST /api/dashboard/planning/start", a.handleDashboardPlanningStart)
		mux.HandleFunc("POST /api/dashboard/planning/{sessionID}/signal", a.handleDashboardPlanningSignal)
		mux.HandleFunc("GET /api/dashboard/stats/{project}", a.handleDashboardStats)
		mux.HandleFunc("GET /api/dashboard/timeline/{project}", a.handleDashboardTimeline)
		mux.HandleFunc("GET /api/dashboard/tree/{project}", a.handleDashboardTree)
		mux.HandleFunc("GET /api/dashboard/overview/{project}", a.handleDashboardOverview)
		mux.HandleFunc("GET /api/dashboard/overview-grouped/{project}", a.handleDashboardOverviewGrouped)
		mux.HandleFunc("POST /api/dashboard/task/{taskID}/pause", a.handleDashboardTaskPause)
		mux.HandleFunc("POST /api/dashboard/task/{taskID}/kill", a.handleDashboardTaskKill)
		mux.HandleFunc("POST /api/dashboard/task/{taskID}/retry", a.handleDashboardTaskRetry)
		mux.HandleFunc("POST /api/dashboard/task/{taskID}/decompose", a.handleDashboardTaskDecompose)
		mux.HandleFunc("POST /api/system/pause", a.handleSystemPause)
		mux.HandleFunc("POST /api/system/resume", a.handleSystemResume)
	}
	if a.Store != nil {
		mux.HandleFunc("GET /api/dashboard/traces/{taskID}", a.handleDashboardTraces)
		mux.HandleFunc("GET /api/dashboard/lessons/{project}", a.handleDashboardLessons)
	}
	if a.LLM != nil {
		mux.HandleFunc("GET /api/dashboard/suggest/{taskID}", a.handleDashboardSuggest)

		// Plan grooming workspace endpoints.
		mux.HandleFunc("GET /api/dashboard/plans/{project}", a.handlePlanList)
		mux.HandleFunc("GET /api/dashboard/plan/{id}", a.handlePlanGet)
		mux.HandleFunc("POST /api/dashboard/plans", a.handlePlanCreate)
		mux.HandleFunc("POST /api/dashboard/plan/{id}/groom", a.handlePlanGroom)

		// Plan decompose/approve/materialize endpoints.
		mux.HandleFunc("POST /api/dashboard/plan/{id}/save", a.handlePlanSave)
		mux.HandleFunc("POST /api/dashboard/plan/{id}/interview", a.handlePlanInterview)
		mux.HandleFunc("POST /api/dashboard/plan/{id}/decompose", a.handlePlanDecompose)
		mux.HandleFunc("POST /api/dashboard/plan/{id}/approve", a.handlePlanApprove)
		mux.HandleFunc("POST /api/dashboard/plan/{id}/materialize", a.handlePlanMaterialize)
	}

	// Interactive planner session endpoints.
	if a.PlanSession != nil {
		mux.HandleFunc("POST /api/dashboard/plan/{id}/session", a.handlePlanSessionCreate)
		mux.HandleFunc("GET /api/dashboard/plan/{id}/session/stream", a.handlePlanSessionStream)
		mux.HandleFunc("POST /api/dashboard/plan/{id}/session/message", a.handlePlanSessionMessage)
		mux.HandleFunc("POST /api/dashboard/plan/{id}/session/extract", a.handlePlanSessionExtract)
		mux.HandleFunc("DELETE /api/dashboard/plan/{id}/session", a.handlePlanSessionDestroy)
	}

	// Health metrics endpoint (queries both DAG and traces databases).
	if a.DAG != nil && a.TracesDB != nil {
		mux.HandleFunc("GET /api/dashboard/health", a.handleDashboardHealthMetrics)
	}

	// Static file serving for dashboard UI.
	if a.WebDir != "" {
		mux.Handle("GET /", http.FileServer(http.Dir(a.WebDir)))
	}

	return mux
}

func (a *API) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req WorkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if ingressBlocked(a.IngressPolicy, r) {
		if a.Engine == nil || !a.Engine.CanSubmitViaBeads(req.Project) {
			a.jsonError(w, "direct submit blocked by beads bridge ingress policy; route through `chum submit`", http.StatusForbidden)
			return
		}
	}

	dispatch := r.URL.Query().Get("dispatch") == "true"

	var id string
	var err error
	if dispatch {
		id, err = a.Engine.SubmitAndDispatch(r.Context(), req)
	} else {
		id, err = a.Engine.Submit(r.Context(), req)
	}
	if err != nil {
		a.Logger.Error("Submit failed", "error", err)
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.jsonOK(w, map[string]string{"task_id": id})
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	if taskID == "" {
		a.jsonError(w, "task_id required", http.StatusBadRequest)
		return
	}

	result, err := a.Engine.GetStatus(r.Context(), taskID)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	a.jsonOK(w, result)
}

func (a *API) handlePending(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if project == "" {
		a.jsonError(w, "project required", http.StatusBadRequest)
		return
	}

	results, err := a.Engine.ListPending(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.jsonOK(w, results)
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	a.jsonOK(w, map[string]string{"status": "ok"})
}

func (a *API) jsonOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		a.Logger.Error("JSON encode failed", "error", err)
	}
}

func (a *API) jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		a.Logger.Error("JSON encode failed", "error", err, "status_code", code)
	}
}

func ingressBlocked(policy string, r *http.Request) bool {
	_ = r // reserved for future request-scoped policy exceptions
	switch policy {
	case "", "legacy":
		return false
	default:
		return true
	}
}

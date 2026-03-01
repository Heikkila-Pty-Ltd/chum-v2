package jarvis

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

// API exposes the Jarvis integration as HTTP endpoints.
// Mount on a local-only port — no auth needed for localhost.
type API struct {
	Engine *Engine
	Logger *slog.Logger
}

// Handler returns an http.Handler with all Jarvis API routes.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/jarvis/submit", a.handleSubmit)
	mux.HandleFunc("POST /api/jarvis/dispatch", a.handleDispatch)
	mux.HandleFunc("GET /api/jarvis/status/{taskID}", a.handleStatus)
	mux.HandleFunc("GET /api/jarvis/pending/{project}", a.handlePending)
	mux.HandleFunc("GET /api/jarvis/health", a.handleHealth)
	return mux
}

func (a *API) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req WorkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
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

func (a *API) handleDispatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID  string      `json:"task_id"`
		Request WorkRequest `json:"request"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := a.Engine.Dispatch(r.Context(), body.TaskID, body.Request); err != nil {
		a.Logger.Error("Dispatch failed", "error", err)
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a.jsonOK(w, map[string]string{"status": "dispatched"})
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
	_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("%s", msg)})
}

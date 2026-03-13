package jarvis

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/plansession"
)

// setSSEHeaders sets standard headers for Server-Sent Events responses.
func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// handlePlanSessionCreate spawns a new planning session for a plan.
// POST /api/dashboard/plan/{id}/session
func (a *API) handlePlanSessionCreate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}

	// Verify plan exists and is not in terminal status.
	plan, err := a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, "plan not found", http.StatusNotFound)
		return
	}
	if plan.Status == "decomposed" || plan.Status == "approved" || plan.Status == "materialized" {
		a.jsonError(w, fmt.Sprintf("plan is in terminal status: %s", plan.Status), http.StatusConflict)
		return
	}

	// Determine work directory from plan's project.
	workDir := "."

	sess, err := a.PlanSession.Spawn(id, workDir)
	if err != nil {
		a.Logger.Error("Failed to spawn plan session", "plan_id", id, "error", err)
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Start the bridge if not already started.
	if sess.Bridge() == nil {
		bridge, err := plansession.NewBridge(sess)
		if err != nil {
			a.Logger.Error("Failed to start bridge", "plan_id", id, "error", err)
			a.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = bridge
	}

	// Update plan status to grooming if still draft.
	if plan.Status == "draft" {
		_ = a.DAG.TransitionPlanStatus(r.Context(), id, "draft", "grooming")
	}

	a.jsonOK(w, map[string]string{
		"session_id": sess.ID,
		"status":     sess.State.String(),
	})
}

// handlePlanSessionStream streams events from a planning session via SSE.
// GET /api/dashboard/plan/{id}/session/stream
func (a *API) handlePlanSessionStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}

	sess, err := a.PlanSession.Get(id)
	if err != nil {
		a.jsonError(w, "no active session", http.StatusNotFound)
		return
	}

	bridge := sess.Bridge()
	if bridge == nil {
		a.jsonError(w, "session bridge not started", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		a.jsonError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	setSSEHeaders(w)

	// Send conversation history on reconnect.
	plan, err := a.DAG.GetPlan(r.Context(), id)
	if err == nil && len(plan.Conversation) > 2 { // > "[]"
		fmt.Fprintf(w, "event: history\ndata: %s\n\n", string(plan.Conversation))
		flusher.Flush()
	}

	// Stream events.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	events := bridge.Events()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				fmt.Fprintf(w, "event: session_destroyed\ndata: %s\n\n",
					mustJSON(map[string]string{"reason": "channel_closed"}))
				flusher.Flush()
				return
			}

			data := mustJSON(event.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()

			// If session is destroyed, stop streaming.
			if event.Type == plansession.EventSessionDestroyed {
				return
			}
		}
	}
}

// handlePlanSessionMessage sends a message to the planning session.
// POST /api/dashboard/plan/{id}/session/message
func (a *API) handlePlanSessionMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		a.jsonError(w, "message required", http.StatusBadRequest)
		return
	}

	sess, err := a.PlanSession.Get(id)
	if err != nil {
		a.jsonError(w, "no active session", http.StatusNotFound)
		return
	}

	bridge := sess.Bridge()
	if bridge == nil {
		a.jsonError(w, "session bridge not started", http.StatusServiceUnavailable)
		return
	}

	// Persist user turn immediately.
	userMsg := dag.ConversationMessage{
		Role:      "user",
		Message:   req.Message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if err := a.DAG.AppendConversation(r.Context(), id, userMsg); err != nil {
		a.Logger.Error("Failed to persist user message", "plan_id", id, "error", err)
	}

	// Send to session.
	if err := bridge.SendMessage(req.Message); err != nil {
		if err == plansession.ErrSessionBusy {
			a.jsonError(w, "Claude is currently responding", http.StatusConflict)
			return
		}
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	a.jsonOK(w, map[string]string{"status": "accepted"})
}

// handlePlanSessionExtract triggers structured plan extraction.
// POST /api/dashboard/plan/{id}/session/extract
func (a *API) handlePlanSessionExtract(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}

	sess, err := a.PlanSession.Get(id)
	if err != nil {
		a.jsonError(w, "no active session", http.StatusNotFound)
		return
	}

	bridge := sess.Bridge()
	if bridge == nil {
		a.jsonError(w, "session bridge not started", http.StatusServiceUnavailable)
		return
	}

	extractPrompt := `Please extract the structured plan from our conversation. Output a JSON object with these fields:
- problem_statement: the core problem being solved
- desired_outcome: what success looks like
- summary: 2-3 sentence summary
- constraints: list of constraints
- assumptions: list of assumptions
- non_goals: what we're explicitly not doing
- risks: identified risks
- open_questions: unresolved questions
- validation_strategy: how to validate the solution
- working_markdown: the full plan document in markdown format`

	if err := bridge.SendMessage(extractPrompt); err != nil {
		if err == plansession.ErrSessionBusy {
			a.jsonError(w, "Claude is currently responding", http.StatusConflict)
			return
		}
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Response will come via SSE — this is async.
	w.WriteHeader(http.StatusAccepted)
	a.jsonOK(w, map[string]string{"status": "extracting"})
}

// handlePlanSessionDestroy manually destroys a planning session.
// DELETE /api/dashboard/plan/{id}/session
func (a *API) handlePlanSessionDestroy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}

	if err := a.PlanSession.Destroy(id); err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	a.jsonOK(w, map[string]string{"status": "destroyed"})
}

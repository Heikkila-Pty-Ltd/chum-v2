package jarvis

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
)

const plannerSystemPrompt = `You are a technical planning facilitator grooming a backlog item.
Your job is to take a rough idea and refine it into a PR-level specification
through conversation.

BEHAVIOR:
- Early in the conversation: ask pointed questions ONE AT A TIME.
  Focus on scope boundaries, edge cases, acceptance criteria, technical constraints.
  Be specific. Don't ask vague questions.
- When you have enough context: produce structured specification sections.
  Include: problem statement, solution approach, acceptance criteria,
  API changes, DB schema, file changes, pseudo-code, test cases.
- When asked to decompose: break into dependency-ordered tasks.
  Each task gets: title, description, acceptance criteria, files to modify, pseudo-code.
- When asked to finalize: confirm the spec is ready for implementation.

TASK HIERARCHY — use epics, tasks, and subtasks:
- Epic: a large deliverable spanning multiple tasks (e.g. "Implement auth system")
- Task: a single unit of work an agent can complete in ≤15 minutes
- Subtask: a finer breakdown within a task if needed (same constraints)
When decomposing, organize work into epics containing tasks. If a task is too
large for one agent pass, break it into subtasks. Mark dependencies between
tasks/epics explicitly.

AVAILABLE TOOLING — the execution engine has these capabilities:
- AST parsing (tree-sitter): extracts symbols (func, method, type, interface,
  const, var) with signatures, receivers, doc comments, line numbers
- Target resolution: tasks reference files and symbols; the admission gate
  resolves these against the actual codebase and detects conflicts
- Conflict fencing: if two tasks touch the same file/symbol, they are
  automatically serialized (lower priority waits)
- Staleness detection: if referenced symbols change between planning and
  execution, the task is flagged stale
- Relevance filtering: embedding-based + keyword fallback to select which
  files an agent sees as context
- Context injection: relevant files get full source, surrounding files get
  signatures only (saves tokens)

PLANNING IMPLICATIONS:
- Reference specific files and symbols in task descriptions — the engine
  resolves them deterministically via AST, not guessing
- Tasks touching the same files will be serialized automatically, so plan
  independent tasks to touch different files when possible
- The admission gate validates: description > 50 chars, acceptance criteria
  present, estimate ≤ 15 minutes. Plan accordingly.
- Agents get filtered codebase context automatically — no need to paste code
  into task descriptions, just reference what to change

Always be direct. Suggest scope cuts when things get too broad.
Flag risks and edge cases proactively.

The conversation history and current spec state follow.`

// handlePlanList returns lightweight plan summaries for a project.
func (a *API) handlePlanList(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if project == "" {
		project = "default"
	}

	plans, err := a.DAG.ListPlans(r.Context(), project)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if plans == nil {
		plans = []*dag.PlanDocSummary{}
	}
	a.jsonOK(w, map[string]any{"plans": plans})
}

// handlePlanGet returns a full plan document.
func (a *API) handlePlanGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}

	plan, err := a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	a.jsonOK(w, plan)
}

// handlePlanCreate creates a new plan document.
func (a *API) handlePlanCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Project string `json:"project"`
		Title   string `json:"title"`
		Brief   string `json:"brief"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Project == "" {
		req.Project = "default"
	}
	if req.Title == "" {
		req.Title = "Untitled Plan"
	}

	plan := &dag.PlanDoc{
		Project: req.Project,
		Title:   req.Title,
		Status:  "draft",
	}

	if err := a.DAG.CreatePlan(r.Context(), plan); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If a brief was provided, add it as the first conversation message.
	if req.Brief != "" {
		msg := dag.ConversationMessage{
			Role:      "user",
			Message:   req.Brief,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := a.DAG.AppendConversation(r.Context(), plan.ID, msg); err != nil {
			a.Logger.Error("Failed to append brief", "error", err, "plan_id", plan.ID)
		}
	}

	a.jsonOK(w, map[string]string{"id": plan.ID})
}

// handlePlanGroom handles the grooming conversation via SSE streaming.
func (a *API) handlePlanGroom(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}

	var req struct {
		Message string `json:"message"`
		Action  string `json:"action"` // optional: "materialize", "export"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Message == "" && req.Action == "" {
		a.jsonError(w, "message or action required", http.StatusBadRequest)
		return
	}

	// 1. Read plan from DB.
	plan, err := a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	// 2. Append user message to conversation.
	if req.Message != "" {
		msg := dag.ConversationMessage{
			Role:      "user",
			Message:   req.Message,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := a.DAG.AppendConversation(r.Context(), id, msg); err != nil {
			a.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 3. Build prompt from conversation history.
	prompt := buildGroomPrompt(plan, req.Message)

	// 4. Update status to grooming if still draft.
	if plan.Status == "draft" {
		plan.Status = "grooming"
		if err := a.DAG.UpdatePlan(r.Context(), plan); err != nil {
			a.Logger.Error("Failed to update plan status", "error", err)
		}
	}

	// 5. Set up SSE response.
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.jsonError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// 6. Stream LLM response.
	chunks, err := llm.RunCLIStream(r.Context(), "claude", "", ".", prompt)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", mustJSON(map[string]string{"error": err.Error()}))
		flusher.Flush()
		return
	}

	var fullText strings.Builder
	var streamErr error
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case chunk, ok := <-chunks:
			if !ok {
				// Channel closed unexpectedly.
				goto done
			}
			if chunk.Done {
				streamErr = chunk.Error
				goto done
			}
			fullText.WriteString(chunk.Text)
			fmt.Fprintf(w, "event: chunk\ndata: %s\n\n", mustJSON(map[string]string{"text": chunk.Text}))
			flusher.Flush()
		}
	}

done:
	// 7. Surface CLI errors to the client.
	if streamErr != nil {
		a.Logger.Error("LLM stream failed", "error", streamErr, "plan_id", id)
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", mustJSON(map[string]string{"error": streamErr.Error()}))
		flusher.Flush()
	}

	// 8. Save assistant response to conversation.
	responseText := strings.TrimSpace(fullText.String())
	if responseText != "" {
		msg := dag.ConversationMessage{
			Role:      "assistant",
			Message:   responseText,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := a.DAG.AppendConversation(r.Context(), id, msg); err != nil {
			a.Logger.Error("Failed to append assistant response", "error", err, "plan_id", id)
		}
	}

	// 9. Re-read plan to get latest state and send done event with full plan.
	updatedPlan, err := a.DAG.GetPlan(r.Context(), id)
	donePayload := map[string]any{
		"full_text": responseText,
		"status":    plan.Status,
	}
	if err == nil {
		donePayload["status"] = updatedPlan.Status
		donePayload["plan"] = updatedPlan
	}
	fmt.Fprintf(w, "event: done\ndata: %s\n\n", mustJSON(donePayload))
	flusher.Flush()
}

// buildGroomPrompt assembles the full prompt for the LLM from conversation history.
func buildGroomPrompt(plan *dag.PlanDoc, latestMessage string) string {
	var sb strings.Builder
	sb.WriteString(plannerSystemPrompt)
	sb.WriteString("\n\n---\n\n")

	// Add spec state if non-empty.
	if len(plan.SpecJSON) > 2 { // more than "{}"
		sb.WriteString("## Current Spec State\n")
		sb.WriteString(string(plan.SpecJSON))
		sb.WriteString("\n\n")
	}

	// Add conversation history.
	sb.WriteString("## Conversation History\n\n")
	var conv []dag.ConversationMessage
	if err := json.Unmarshal(plan.Conversation, &conv); err == nil {
		for _, msg := range conv {
			if msg.Role == "user" {
				sb.WriteString("**User:** ")
			} else {
				sb.WriteString("**Planner:** ")
			}
			sb.WriteString(msg.Message)
			sb.WriteString("\n\n")
		}
	}

	// Add latest message if not already in conversation.
	if latestMessage != "" {
		sb.WriteString("**User:** ")
		sb.WriteString(latestMessage)
		sb.WriteString("\n\nRespond as the Planner:")
	}

	return sb.String()
}

// mustJSON marshals to JSON, panicking on error (should never happen with simple types).
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"json marshal failed"}`
	}
	return string(b)
}

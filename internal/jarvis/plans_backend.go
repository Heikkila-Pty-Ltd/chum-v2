package jarvis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/codebase"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// --- Save handler ---

func (a *API) handlePlanSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}
	var body struct {
		Title      string `json:"title"`
		GoalTaskID string `json:"goal_task_id"`
		Markdown   string `json:"markdown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	fields := map[string]any{
		"title":            body.Title,
		"goal_task_id":     body.GoalTaskID,
		"working_markdown": body.Markdown,
	}
	if err := a.DAG.UpdatePlanFields(r.Context(), id, fields); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan, err := a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, plan)
}

// --- LLM handlers ---

const interviewSystemPrompt = `You are a planning assistant helping decompose work into well-specified tasks.
You are interviewing a human to refine a plan. Ask ONE focused question at a time.
Build on previous answers. Keep questions concise and specific.

You MUST respond with a JSON object (no markdown fences, no commentary outside the JSON):
{
  "reply": "Your conversational response acknowledging their answer",
  "next_question": "Your next focused question (empty string if plan is complete)",
  "structured": {
    "problem_statement": "What problem are we solving (update as you learn more)",
    "desired_outcome": "What does success look like",
    "summary": "Current plan summary",
    "constraints": ["Known constraints"],
    "assumptions": ["Assumptions we're making"],
    "non_goals": ["What is explicitly out of scope"],
    "risks": ["Identified risks"],
    "open_questions": ["Remaining unknowns"],
    "validation_strategy": ["How we'll verify success"]
  },
  "working_markdown": "Updated plan document in markdown (evolve this as the conversation progresses)"
}

Populate structured fields progressively — start with what you can infer, refine as you learn more.
If the user's brief is comprehensive, you can fill in most fields on the first pass.`

func (a *API) handlePlanInterview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		a.jsonError(w, "message required", http.StatusBadRequest)
		return
	}

	plan, err := a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	// Parse existing conversation.
	var convo []map[string]string
	if len(plan.Conversation) > 0 {
		_ = json.Unmarshal(plan.Conversation, &convo)
	}
	convo = append(convo, map[string]string{"role": "user", "message": body.Message})

	// Build prompt with context.
	briefContext := plan.WorkingMarkdown
	if briefContext == "" {
		briefContext = plan.BriefMarkdown
	}

	// Resolve workDir for this project.
	workDir := "."
	if a.Engine != nil && plan.Project != "" {
		if wd := a.Engine.WorkDir(plan.Project); wd != "" {
			workDir = wd
		}
	}

	// Gather codebase context (first turn) or reuse cached.
	var ctxFormatted string
	if plan.ContextSnapshot != "" {
		ctxFormatted = plan.ContextSnapshot
	} else {
		ctxResult := codebase.Build(r.Context(), codebase.BuildOpts{
			Parser:  a.AST,
			Store:   a.Store,
			DAG:     a.DAG,
			Logger:  a.Logger,
			WorkDir: workDir,
			Project: plan.Project,
			Query:   briefContext + " " + body.Message,
		})
		ctxFormatted = codebase.FormatForPrompt(ctxResult)
		// Cache for subsequent turns.
		if ctxFormatted != "" {
			_ = a.DAG.UpdatePlanFields(r.Context(), id, map[string]any{
				"context_snapshot": ctxFormatted,
			})
		}
	}

	var sb strings.Builder
	sb.WriteString(interviewSystemPrompt)
	if ctxFormatted != "" {
		sb.WriteString("\n\n--- CODEBASE CONTEXT ---\n")
		sb.WriteString(ctxFormatted)
	}
	sb.WriteString("\n\n--- PLAN BRIEF ---\n")
	sb.WriteString(briefContext)
	sb.WriteString("\n\n--- CONVERSATION SO FAR ---\n")
	for _, turn := range convo {
		sb.WriteString(fmt.Sprintf("%s: %s\n\n", turn["role"], turn["message"]))
	}

	result, err := a.LLM.Plan(r.Context(), "claude", "claude-sonnet-4-20250514", workDir, sb.String())
	if err != nil {
		a.jsonError(w, fmt.Sprintf("LLM call failed: %v", err), http.StatusInternalServerError)
		return
	}

	rawJSON := llm.ExtractJSON(result.Output)
	fields := map[string]any{}

	var parsed struct {
		Reply           string          `json:"reply"`
		NextQuestion    string          `json:"next_question"`
		Structured      json.RawMessage `json:"structured"`
		WorkingMarkdown string          `json:"working_markdown"`
	}
	if rawJSON != "" && json.Unmarshal([]byte(rawJSON), &parsed) == nil {
		assistantMsg := parsed.Reply
		if assistantMsg == "" {
			assistantMsg = result.Output
		}
		convo = append(convo, map[string]string{"role": "assistant", "message": assistantMsg})
		fields["next_question"] = parsed.NextQuestion
		fields["planner_reply"] = parsed.Reply
		if len(parsed.Structured) > 0 {
			fields["structured"] = parsed.Structured
		}
		if parsed.WorkingMarkdown != "" {
			fields["working_markdown"] = parsed.WorkingMarkdown
		}
	} else {
		output := llm.UnwrapClaudeJSON(result.Output)
		if output == "" {
			output = result.Output
		}
		convo = append(convo, map[string]string{"role": "assistant", "message": output})
		fields["planner_reply"] = output
		fields["next_question"] = ""
	}

	convoJSON, _ := json.Marshal(convo)
	fields["conversation"] = json.RawMessage(convoJSON)
	fields["status"] = "needs_input"

	if err := a.DAG.UpdatePlanFields(r.Context(), id, fields); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan, err = a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, plan)
}

const decomposeSystemPrompt = `You are a task extraction engine for a software engineering AI agent system.
The grooming conversation below already contains a detailed task breakdown produced by a planner.
Your job is to EXTRACT those tasks into structured JSON — not to invent new ones.

RULES:
- Extract EVERY task from the conversation's task breakdown
- Preserve ALL detail: full descriptions, file paths, pseudo-code, acceptance criteria
- The description field should include EVERYTHING from the original task: description text,
  files to modify/read, pseudo-code, technical notes. Combine all into one rich description.
  It must be >50 characters.
- The acceptance field must contain the original acceptance criteria verbatim
- Estimate each task in minutes (max 15m per task; if the original implies longer, split it
  into subtasks)
- Map dependency references: if Task 3 says "after Task 1", depends_on should be ["T1"]

HIERARCHY — use type field:
- "epic": a group of related tasks (e.g. "Remove timeline views"). Epics don't execute
  directly — they track completion of their children.
- "task": a single unit of work an agent executes (≤15 minutes). Must have description >50
  chars and acceptance criteria.
- "subtask": a finer breakdown within a task. Same constraints as task.
- Set parent_ref to link tasks to their epic, or subtasks to their task.
- Set children to list the refs contained in an epic or task.

Use refs like E1, T1, T2, T1.1, T1.2... (E for epics, T for tasks, T1.1 for subtasks).

You MUST respond with a JSON object (no markdown fences, no commentary outside the JSON):
{
  "tasks": [
    {
      "ref": "E1",
      "title": "Epic title",
      "type": "epic",
      "description": "Epic-level description",
      "acceptance": "All children complete",
      "estimate_minutes": 0,
      "depends_on": [],
      "children": ["T1", "T2"]
    },
    {
      "ref": "T1",
      "title": "Task title",
      "type": "task",
      "description": "Full detailed description preserving all context",
      "acceptance": "Acceptance criteria from the original task",
      "estimate_minutes": 10,
      "depends_on": [],
      "parent_ref": "E1"
    }
  ]
}

IMPORTANT: Do NOT summarize or simplify. Preserve all richness from the conversation.
Reference specific files and symbols — the engine resolves them via AST.`

func (a *API) handlePlanDecompose(w http.ResponseWriter, r *http.Request) {
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

	// Resolve workDir for this project.
	workDir := "."
	if a.Engine != nil && plan.Project != "" {
		if wd := a.Engine.WorkDir(plan.Project); wd != "" {
			workDir = wd
		}
	}

	// Gather codebase context — reuse cached from interview if available.
	var ctxFormatted string
	if plan.ContextSnapshot != "" {
		ctxFormatted = plan.ContextSnapshot
	} else {
		specDoc := plan.WorkingMarkdown
		if specDoc == "" {
			specDoc = plan.BriefMarkdown
		}
		ctxResult := codebase.Build(r.Context(), codebase.BuildOpts{
			Parser:  a.AST,
			Store:   a.Store,
			DAG:     a.DAG,
			Logger:  a.Logger,
			WorkDir: workDir,
			Project: plan.Project,
			Query:   specDoc,
		})
		ctxFormatted = codebase.FormatForPrompt(ctxResult)
	}

	// Build context from all available sources: working markdown, brief,
	// structured analysis, and — critically — the conversation itself,
	// which is where the real spec lives after grooming.
	var sb strings.Builder
	sb.WriteString(decomposeSystemPrompt)
	if ctxFormatted != "" {
		sb.WriteString("\n\n--- CODEBASE CONTEXT ---\n")
		sb.WriteString(ctxFormatted)
	}

	if plan.WorkingMarkdown != "" {
		sb.WriteString("\n\n--- PLAN DOCUMENT ---\n")
		sb.WriteString(plan.WorkingMarkdown)
	} else if plan.BriefMarkdown != "" {
		sb.WriteString("\n\n--- PLAN BRIEF ---\n")
		sb.WriteString(plan.BriefMarkdown)
	}

	if len(plan.SpecJSON) > 2 {
		sb.WriteString("\n\n--- STRUCTURED SPEC ---\n")
		sb.WriteString(string(plan.SpecJSON))
	}

	if len(plan.Structured) > 0 && string(plan.Structured) != "{}" {
		sb.WriteString("\n\n--- STRUCTURED ANALYSIS ---\n")
		sb.WriteString(string(plan.Structured))
	}

	// Include conversation — this is where the grooming session lives.
	var convo []dag.ConversationMessage
	if err := json.Unmarshal(plan.Conversation, &convo); err == nil && len(convo) > 0 {
		sb.WriteString("\n\n--- GROOMING CONVERSATION ---\n")
		for _, msg := range convo {
			if msg.Role == "user" {
				sb.WriteString("**User:** ")
			} else {
				sb.WriteString("**Planner:** ")
			}
			sb.WriteString(msg.Message)
			sb.WriteString("\n\n")
		}
	}

	a.Logger.Info("Decompose: calling LLM", "plan_id", id, "prompt_len", sb.Len())

	result, err := a.LLM.Plan(r.Context(), "claude", "claude-sonnet-4-20250514", workDir, sb.String())
	if err != nil {
		a.jsonError(w, fmt.Sprintf("LLM call failed: %v", err), http.StatusInternalServerError)
		return
	}

	a.Logger.Info("Decompose: LLM returned", "plan_id", id, "output_len", len(result.Output))

	rawJSON := llm.ExtractJSON(result.Output)
	if rawJSON == "" {
		// Log the raw output so we can debug extraction failures.
		preview := result.Output
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		a.Logger.Error("Decompose: no JSON extracted from LLM output", "plan_id", id, "output_preview", preview)
		a.jsonError(w, "LLM did not produce valid JSON; check server logs", http.StatusInternalServerError)
		return
	}

	var parsed struct {
		Tasks []dag.DraftTask `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		a.Logger.Error("Decompose: JSON parse failed", "plan_id", id, "error", err, "json_preview", rawJSON[:min(500, len(rawJSON))])
		a.jsonError(w, fmt.Sprintf("failed to parse task decomposition: %v", err), http.StatusInternalServerError)
		return
	}
	if len(parsed.Tasks) == 0 {
		a.Logger.Error("Decompose: parsed JSON has zero tasks", "plan_id", id, "json_preview", rawJSON[:min(500, len(rawJSON))])
		a.jsonError(w, "LLM produced zero tasks", http.StatusInternalServerError)
		return
	}

	batches := computeBatches(parsed.Tasks)
	refToBatch := map[string]int{}
	for _, b := range batches {
		for _, ref := range b.Refs {
			refToBatch[ref] = b.Index
		}
	}
	for i := range parsed.Tasks {
		parsed.Tasks[i].Batch = refToBatch[parsed.Tasks[i].Ref]
	}

	tasksJSON, _ := json.Marshal(parsed.Tasks)
	batchesJSON, _ := json.Marshal(batches)

	fields := map[string]any{
		"draft_tasks":       json.RawMessage(tasksJSON),
		"execution_batches": json.RawMessage(batchesJSON),
		"status":            "decomposed",
	}
	if err := a.DAG.UpdatePlanFields(r.Context(), id, fields); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan, err = a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, plan)
}

type executionBatch struct {
	Index int      `json:"index"`
	Refs  []string `json:"refs"`
}

func computeBatches(tasks []dag.DraftTask) []executionBatch {
	batchNum := map[string]int{}
	refSet := map[string]bool{}
	for _, t := range tasks {
		refSet[t.Ref] = true
		batchNum[t.Ref] = 0
	}
	for range tasks {
		for _, t := range tasks {
			for _, dep := range t.DependsOn {
				if !refSet[dep] {
					continue
				}
				if candidate := batchNum[dep] + 1; candidate > batchNum[t.Ref] {
					batchNum[t.Ref] = candidate
				}
			}
		}
	}

	maxBatch := 0
	for _, b := range batchNum {
		if b > maxBatch {
			maxBatch = b
		}
	}
	batches := make([]executionBatch, maxBatch+1)
	for i := range batches {
		batches[i] = executionBatch{Index: i, Refs: []string{}}
	}
	for _, t := range tasks {
		b := batchNum[t.Ref]
		batches[b].Refs = append(batches[b].Refs, t.Ref)
	}
	return batches
}

// --- Approve + Materialize ---

func (a *API) handlePlanApprove(w http.ResponseWriter, r *http.Request) {
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
	if plan.Status != "decomposed" {
		a.jsonError(w, fmt.Sprintf("plan must be in 'decomposed' status to approve (current: %s)", plan.Status), http.StatusBadRequest)
		return
	}

	var draftTasks []dag.DraftTask
	if err := json.Unmarshal(plan.DraftTasks, &draftTasks); err != nil || len(draftTasks) == 0 {
		a.jsonError(w, "no draft tasks to approve", http.StatusBadRequest)
		return
	}

	totalEstimate := 0
	for _, dt := range draftTasks {
		totalEstimate += dt.EstimateMinutes
	}
	var batches []executionBatch
	_ = json.Unmarshal(plan.ExecutionBatches, &batches)

	// Validate every draft task against admission gate constraints.
	// Reject approval if any task violates hard requirements.
	var riskFlags []string
	var rejectReasons []string
	for _, dt := range draftTasks {
		if dt.Type == "epic" {
			continue // epics don't execute directly
		}
		if len(dt.Description) <= 50 {
			rejectReasons = append(rejectReasons, fmt.Sprintf("%s: description too short (%d chars, need >50)", dt.Ref, len(dt.Description)))
		}
		if dt.Acceptance == "" {
			rejectReasons = append(rejectReasons, fmt.Sprintf("%s: missing acceptance criteria", dt.Ref))
		}
		if dt.EstimateMinutes > 15 {
			rejectReasons = append(rejectReasons, fmt.Sprintf("%s: estimate %dm exceeds 15m limit", dt.Ref, dt.EstimateMinutes))
		}
	}
	if len(rejectReasons) > 0 {
		a.jsonError(w, fmt.Sprintf("draft tasks fail admission gate: %s", strings.Join(rejectReasons, "; ")), http.StatusUnprocessableEntity)
		return
	}

	if err := a.DAG.UpdatePlanFields(r.Context(), id, map[string]any{"status": "approved"}); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan, err = a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, map[string]any{
		"plan": plan,
		"review_summary": map[string]any{
			"task_count":     len(draftTasks),
			"total_estimate": totalEstimate,
			"batch_count":    len(batches),
			"risk_flags":     riskFlags,
		},
	})
}

func (a *API) handlePlanMaterialize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.jsonError(w, "plan id required", http.StatusBadRequest)
		return
	}
	// Atomically transition approved→materializing to prevent double-click races.
	// If another request already started materializing, this returns an error.
	if err := a.DAG.TransitionPlanStatus(r.Context(), id, "approved", "materializing"); err != nil {
		a.jsonError(w, fmt.Sprintf("cannot materialize: %v", err), http.StatusConflict)
		return
	}

	plan, err := a.DAG.GetPlan(r.Context(), id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	var draftTasks []dag.DraftTask
	if err := json.Unmarshal(plan.DraftTasks, &draftTasks); err != nil || len(draftTasks) == 0 {
		// Roll back status on validation failure.
		_ = a.DAG.UpdatePlanFields(r.Context(), id, map[string]any{"status": "approved"})
		a.jsonError(w, "no draft tasks to materialize", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Enforce beads-first ingress policy: if the policy requires beads,
	// we must have a working beads client. No silent fallback to direct DAG writes.
	requiresBeads := a.IngressPolicy != "" && a.IngressPolicy != "legacy"
	var bc beads.Store
	if a.Engine != nil {
		bc = a.Engine.BeadsClient(plan.Project)
	}
	if requiresBeads && bc == nil {
		_ = a.DAG.UpdatePlanFields(ctx, id, map[string]any{"status": "approved"})
		a.jsonError(w, fmt.Sprintf("beads ingress policy %q requires a beads client for project %q; cannot fall back to direct DAG writes", a.IngressPolicy, plan.Project), http.StatusFailedDependency)
		return
	}

	var goalID string
	if bc != nil {
		goalID, err = a.materializeViaBeads(ctx, bc, plan, draftTasks)
	} else {
		// Only reachable under "legacy" ingress policy.
		goalID, err = a.materializeViaDAG(ctx, plan, draftTasks)
	}
	if err != nil {
		// Roll back to approved so user can retry.
		_ = a.DAG.UpdatePlanFields(ctx, id, map[string]any{"status": "approved"})
		a.jsonError(w, fmt.Sprintf("materialization failed: %v", err), http.StatusInternalServerError)
		return
	}

	if err := a.DAG.UpdatePlanFields(ctx, id, map[string]any{
		"materialized_goal_id": goalID,
		"status":               "materialized",
	}); err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan, err = a.DAG.GetPlan(ctx, id)
	if err != nil {
		a.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.jsonOK(w, plan)
}

// validateDraftTaskRefs checks that all depends_on and parent_ref references
// point to refs that exist in the task list. Must be called before any writes.
func validateDraftTaskRefs(tasks []dag.DraftTask) error {
	refSet := map[string]bool{}
	for _, t := range tasks {
		if refSet[t.Ref] {
			return fmt.Errorf("duplicate task ref %q", t.Ref)
		}
		refSet[t.Ref] = true
	}
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if !refSet[dep] {
				return fmt.Errorf("task %s depends on unknown ref %q", t.Ref, dep)
			}
		}
		if t.ParentRef != "" && !refSet[t.ParentRef] {
			return fmt.Errorf("task %s has unknown parent_ref %q", t.Ref, t.ParentRef)
		}
	}
	return nil
}

// topoSortDraftTasks orders tasks so that parents appear before children.
// Tasks with a parent_ref are placed after their parent. Tasks without
// parent_ref keep their original relative order. Detects cycles.
func topoSortDraftTasks(tasks []dag.DraftTask) ([]dag.DraftTask, error) {
	byRef := map[string]int{}
	for i, t := range tasks {
		byRef[t.Ref] = i
	}

	const (
		unvisited  = 0
		inProgress = 1
		done       = 2
	)
	state := make([]int, len(tasks))
	result := make([]dag.DraftTask, 0, len(tasks))

	var visit func(i int) error
	visit = func(i int) error {
		if state[i] == done {
			return nil
		}
		if state[i] == inProgress {
			return fmt.Errorf("parent_ref cycle detected at %q", tasks[i].Ref)
		}
		state[i] = inProgress
		// Visit parent first if it exists.
		if pr := tasks[i].ParentRef; pr != "" {
			if pi, ok := byRef[pr]; ok {
				if err := visit(pi); err != nil {
					return err
				}
			}
		}
		state[i] = done
		result = append(result, tasks[i])
		return nil
	}
	for i := range tasks {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (a *API) materializeViaBeads(ctx context.Context, bc beads.Store, plan *dag.PlanDoc, draftTasks []dag.DraftTask) (string, error) {
	// Validate all refs before any writes to prevent partial materialization.
	if err := validateDraftTaskRefs(draftTasks); err != nil {
		return "", fmt.Errorf("ref validation: %w", err)
	}

	// Create top-level plan epic in beads.
	planEpicID, err := bc.Create(ctx, beads.CreateParams{
		Title:     plan.Title,
		IssueType: "epic",
		Labels:    []string{"plan-generated"},
		Priority:  -1,
	})
	if err != nil {
		return "", fmt.Errorf("create plan epic: %w", err)
	}

	// Mirror the epic into DAG immediately (don't wait for scanner).
	if _, err := a.DAG.CreateTask(ctx, dag.Task{
		ID:      planEpicID,
		Title:   plan.Title,
		Type:    "goal",
		Status:  string(types.StatusReady),
		Project: plan.Project,
		Labels:  []string{"plan-generated"},
	}); err != nil {
		return "", fmt.Errorf("mirror plan epic to DAG: %w", err)
	}
	a.DAG.UpsertBeadsMapping(ctx, plan.Project, planEpicID, planEpicID, "")

	// Sort so parents are created before children.
	sorted, err := topoSortDraftTasks(draftTasks)
	if err != nil {
		return "", fmt.Errorf("task ordering: %w", err)
	}

	// Two passes: first create all items, then wire dependencies.
	refToIssueID := map[string]string{}
	for _, dt := range sorted {
		// Determine parent: if parent_ref set, it must already be created
		// (guaranteed by topoSort). Otherwise fall back to the plan epic.
		beadsParentID := planEpicID
		dagParentID := planEpicID
		if dt.ParentRef != "" {
			if pid, ok := refToIssueID[dt.ParentRef]; ok {
				beadsParentID = pid
				dagParentID = pid
			}
		}

		issueType := "task"
		dagType := "task"
		if dt.Type == "epic" {
			issueType = "epic"
			dagType = "goal"
		}

		issueID, err := bc.Create(ctx, beads.CreateParams{
			Title:            dt.Title,
			Description:      dt.Description,
			Acceptance:       dt.Acceptance,
			EstimatedMinutes: dt.EstimateMinutes,
			IssueType:        issueType,
			ParentID:         beadsParentID,
			Labels:           []string{"plan-generated"},
			Priority:         -1,
		})
		if err != nil {
			return "", fmt.Errorf("create issue for %s: %w", dt.Ref, err)
		}
		refToIssueID[dt.Ref] = issueID

		// Mirror into DAG immediately.
		if _, err := a.DAG.CreateTask(ctx, dag.Task{
			ID:              issueID,
			Title:           dt.Title,
			Description:     dt.Description,
			Acceptance:      dt.Acceptance,
			EstimateMinutes: dt.EstimateMinutes,
			Type:            dagType,
			ParentID:        dagParentID,
			Status:          string(types.StatusReady),
			Project:         plan.Project,
			Labels:          []string{"plan-generated"},
		}); err != nil {
			return "", fmt.Errorf("mirror task %s to DAG: %w", dt.Ref, err)
		}
		a.DAG.UpsertBeadsMapping(ctx, plan.Project, issueID, issueID, "")
	}

	// Wire dependencies in both beads and DAG.
	for _, dt := range draftTasks {
		for _, depRef := range dt.DependsOn {
			depIssueID, ok := refToIssueID[depRef]
			if !ok {
				return "", fmt.Errorf("task %s depends on unknown ref %q", dt.Ref, depRef)
			}
			if err := bc.AddDependency(ctx, refToIssueID[dt.Ref], depIssueID); err != nil {
				return "", fmt.Errorf("add beads dep %s->%s: %w", dt.Ref, depRef, err)
			}
			if err := a.DAG.AddEdgeWithSource(ctx, refToIssueID[dt.Ref], depIssueID, "plan"); err != nil {
				return "", fmt.Errorf("add DAG edge %s->%s: %w", dt.Ref, depRef, err)
			}
		}
	}
	return planEpicID, nil
}

func (a *API) materializeViaDAG(ctx context.Context, plan *dag.PlanDoc, draftTasks []dag.DraftTask) (string, error) {
	// Validate all refs before any writes to prevent partial materialization.
	if err := validateDraftTaskRefs(draftTasks); err != nil {
		return "", fmt.Errorf("ref validation: %w", err)
	}

	// Create top-level goal.
	goalID, err := a.DAG.CreateTask(ctx, dag.Task{
		Title:   plan.Title,
		Type:    "goal",
		Status:  string(types.StatusReady),
		Project: plan.Project,
		Labels:  []string{"plan-generated"},
	})
	if err != nil {
		return "", fmt.Errorf("create goal task: %w", err)
	}

	// Sort so parents are created before children.
	sorted, err := topoSortDraftTasks(draftTasks)
	if err != nil {
		return "", fmt.Errorf("task ordering: %w", err)
	}

	// Two passes: create all, then wire dependencies.
	refToTaskID := map[string]string{}
	for _, dt := range sorted {
		// Determine parent: parent_ref → already-created item, else goal.
		parentID := goalID
		if dt.ParentRef != "" {
			if pid, ok := refToTaskID[dt.ParentRef]; ok {
				parentID = pid
			}
		}

		taskType := "task"
		if dt.Type == "epic" {
			taskType = "goal"
		}

		taskID, err := a.DAG.CreateTask(ctx, dag.Task{
			Title:           dt.Title,
			Description:     dt.Description,
			Acceptance:      dt.Acceptance,
			EstimateMinutes: dt.EstimateMinutes,
			Type:            taskType,
			ParentID:        parentID,
			Status:          string(types.StatusReady),
			Project:         plan.Project,
			Labels:          []string{"plan-generated"},
		})
		if err != nil {
			return "", fmt.Errorf("create task for %s: %w", dt.Ref, err)
		}
		refToTaskID[dt.Ref] = taskID
	}

	// Wire dependencies.
	for _, dt := range draftTasks {
		for _, depRef := range dt.DependsOn {
			depTaskID, ok := refToTaskID[depRef]
			if !ok {
				return "", fmt.Errorf("task %s depends on unknown ref %q", dt.Ref, depRef)
			}
			if err := a.DAG.AddEdgeWithSource(ctx, refToTaskID[dt.Ref], depTaskID, "plan"); err != nil {
				return "", fmt.Errorf("add edge %s->%s: %w", dt.Ref, depRef, err)
			}
		}
	}
	return goalID, nil
}

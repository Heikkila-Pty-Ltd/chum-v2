package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"go.temporal.io/sdk/activity"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beadsbridge"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/notify"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// PlanningActivities holds dependencies for planning Temporal activities.
type PlanningActivities struct {
	DAG          dag.TaskStore
	Decisions    dag.DecisionStore
	Planning     dag.PlanningStore
	Config       *config.Config
	Logger       *slog.Logger
	AST          *astpkg.Parser
	BeadsClients map[string]beads.Store
	ChatSend     notify.ChatSender
	LLM          llm.Runner
}

// StorePlanningSnapshotActivity persists the latest planning snapshot for operator visibility.
func (pa *PlanningActivities) StorePlanningSnapshotActivity(ctx context.Context, snapshot types.PlanningSnapshot) error {
	if pa.Planning == nil {
		return nil
	}
	return pa.Planning.UpsertPlanningSnapshot(ctx, snapshot)
}

// ClarifyGoalActivity runs an LLM to extract intent, constraints, and rationale from the task.
func (pa *PlanningActivities) ClarifyGoalActivity(ctx context.Context, req PlanningRequest) (*ClarifiedGoal, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Clarifying goal", "GoalID", req.GoalID)

	// Fetch goal description from beads if we have it
	goalDesc := ""
	if bc := pa.beadsClient(req.Project); bc != nil && req.GoalID != "" {
		issue, err := bc.Show(ctx, req.GoalID)
		if err == nil {
			goalDesc = issue.Title + "\n" + issue.Description
			if issue.AcceptanceCriteria != "" {
				goalDesc += "\nAcceptance: " + issue.AcceptanceCriteria
			}
		}
	}
	if goalDesc == "" {
		goalDesc = req.GoalID
	}

	codeContext := pa.buildCodebaseContextForTask(ctx, req.WorkDir, goalDesc)
	prompt := fmt.Sprintf(`You are a senior software architect. Analyze this goal and extract the core intent.

GOAL:
%s

CODEBASE:
%s

OUTPUT CONTRACT (strict JSON):
{"intent": "what the user wants to achieve", "constraints": ["constraint1", "constraint2"], "why": "the underlying reason/motivation", "raw": "original description"}

Rules:
- intent: One clear sentence describing the desired outcome
- constraints: Technical or business constraints that bound the solution
- why: The motivation behind the goal (not just what, but why)
- Output ONLY the JSON object. No commentary.`, goalDesc, codeContext)

	result, err := pa.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("clarify goal CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return &ClarifiedGoal{Intent: goalDesc, Raw: goalDesc}, nil
	}

	var goal ClarifiedGoal
	if err := json.Unmarshal([]byte(jsonStr), &goal); err != nil {
		logger.Warn("Failed to parse goal clarification", "error", err)
		return &ClarifiedGoal{Intent: goalDesc, Raw: goalDesc}, nil
	}
	if goal.Raw == "" {
		goal.Raw = goalDesc
	}
	return &goal, nil
}

// ResearchApproachesActivity runs an LLM with full codebase access to research approaches.
func (pa *PlanningActivities) ResearchApproachesActivity(ctx context.Context, req PlanningRequest, goal ClarifiedGoal) ([]ResearchedApproach, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Researching approaches", "GoalID", req.GoalID)

	codeContext := pa.buildCodebaseContextForTask(ctx, req.WorkDir, goal.Intent)
	prompt := fmt.Sprintf(`You are a senior software architect conducting a thorough design review.

GOAL: %s
WHY: %s
CONSTRAINTS: %s

CODEBASE:
%s

Research and propose 3-5 distinct approaches to achieve this goal. For each approach:
1. Analyze the codebase summary above to understand existing patterns and constraints
2. Consider different architectural approaches
3. Evaluate tradeoffs (complexity, risk, reuse, maintenance)
4. Estimate confidence of success (0.0-1.0)

OUTPUT CONTRACT (strict JSON array):
[{"title": "...", "description": "detailed how", "tradeoffs": "pros and cons", "confidence": 0.75, "rank": 1}]

Rules:
- Approaches must be genuinely different strategies, not variations of the same idea
- Rank by confidence (highest first)
- Each approach must be independently viable
- Consider what already exists in the codebase that can be reused
- Output ONLY the JSON array. No commentary.`,
		goal.Intent,
		goal.Why,
		strings.Join(goal.Constraints, ", "),
		codeContext)

	// Research is read-only — use RunCLI (--print) to prevent file mutations.
	result, err := pa.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("research CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("research produced no JSON output")
	}

	approaches, err := parseApproaches(jsonStr)
	if err != nil {
		logger.Error("Research JSON parse failed", "json_excerpt", types.Truncate(jsonStr, 500), "error", err)
		return nil, fmt.Errorf("parse research output: %w", err)
	}

	for i := range approaches {
		approaches[i].Status = "exploring"
		if approaches[i].Rank == 0 {
			approaches[i].Rank = i + 1
		}
		// Generate a temporary ID so the approach is addressable before bead storage.
		// StoreApproachesActivity will overwrite with the real bead ID.
		if approaches[i].ID == "" {
			approaches[i].ID = fmt.Sprintf("approach-%d", approaches[i].Rank)
		}
	}

	logger.Info("Research complete", "Approaches", len(approaches))
	return approaches, nil
}

// GoalCheckActivity validates that researched approaches still serve the original intent.
func (pa *PlanningActivities) GoalCheckActivity(ctx context.Context, req PlanningRequest, goal ClarifiedGoal, approaches []ResearchedApproach) ([]ResearchedApproach, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Goal check", "Approaches", len(approaches))

	approachJSON, err := json.Marshal(approaches)
	if err != nil {
		return nil, fmt.Errorf("marshal approaches for goal check: %w", err)
	}
	prompt := fmt.Sprintf(`You are reviewing research approaches for goal alignment.

ORIGINAL GOAL: %s
WHY: %s

APPROACHES:
%s

For each approach, verify it actually serves the goal. If an approach has drifted from the intent, adjust its confidence downward. If an approach is well-aligned, confirm or slightly adjust confidence.

OUTPUT CONTRACT (strict JSON array — same structure as input, with potentially adjusted confidence values):
[{"title": "...", "description": "...", "tradeoffs": "...", "confidence": 0.X, "rank": N, "status": "exploring"}]

Output ONLY the JSON array.`, goal.Intent, goal.Why, string(approachJSON))

	result, err := pa.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return approaches, fmt.Errorf("goal check CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return approaches, nil
	}

	var checked []ResearchedApproach
	if err := json.Unmarshal([]byte(jsonStr), &checked); err != nil {
		logger.Warn("Goal check returned invalid JSON, keeping original approaches", "error", err)
		return approaches, nil
	}

	// Preserve IDs from original approaches by matching on rank.
	// The LLM may reorder or change the count, so index-based matching is unsafe.
	origByRank := make(map[int]string, len(approaches))
	for _, a := range approaches {
		if a.Rank > 0 && a.ID != "" {
			origByRank[a.Rank] = a.ID
		}
	}
	for i := range checked {
		if id, ok := origByRank[checked[i].Rank]; ok {
			checked[i].ID = id
		}
		// No index-based fallback — unmatched approaches keep whatever ID the LLM returned
		// (or empty). This prevents silent ID misassignment when the LLM reorders approaches.
		if checked[i].Status == "" {
			checked[i].Status = "exploring"
		}
	}

	return checked, nil
}

// StoreApproachesActivity stores researched approaches as beads linked to the goal.
func (pa *PlanningActivities) StoreApproachesActivity(ctx context.Context, req PlanningRequest, approaches []ResearchedApproach) ([]ResearchedApproach, error) {
	logger := activity.GetLogger(ctx)
	bc := pa.beadsClient(req.Project)
	if bc == nil {
		logger.Warn("No beads client, skipping approach storage")
		return approaches, nil
	}

	for i := range approaches {
		a := &approaches[i]
		desc := fmt.Sprintf("%s\n\nTradeoffs: %s\nConfidence: %.0f%%",
			a.Description, a.Tradeoffs, a.Confidence*100)

		id, err := bc.Create(ctx, beads.CreateParams{
			Title:       a.Title,
			Description: desc,
			IssueType:   "task",
			Priority:    2,
			Labels:      []string{"approach", fmt.Sprintf("rank-%d", a.Rank)},
			ParentID:    req.GoalID,
		})
		if err != nil {
			logger.Warn("Failed to store approach bead", "title", a.Title, "error", err)
			continue
		}
		a.ID = id
		logger.Info("Stored approach bead", "ID", id, "Title", a.Title)
	}

	return approaches, nil
}

// DeeperResearchActivity runs a focused deep-dive on a specific approach.
func (pa *PlanningActivities) DeeperResearchActivity(ctx context.Context, req PlanningRequest, approach ResearchedApproach, feedback string) (*ResearchedApproach, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Deeper research", "Approach", approach.Title, "Feedback", feedback)

	codeContext := pa.buildCodebaseContextForTask(ctx, req.WorkDir, approach.Title+" "+approach.Description)
	prompt := fmt.Sprintf(`You are doing a deep-dive research on a specific approach.

APPROACH: %s
DESCRIPTION: %s
TRADEOFFS: %s
CURRENT CONFIDENCE: %.0f%%

USER FEEDBACK: %s

CODEBASE:
%s

Investigate this approach thoroughly based on the codebase summary above:
1. Analyze relevant code paths and structures
2. Identify specific files/functions that would need changes
3. Surface any hidden complexity or risks
4. Refine the confidence based on deeper understanding

OUTPUT CONTRACT (strict JSON):
{"title": "...", "description": "updated detailed description", "tradeoffs": "updated tradeoffs", "confidence": 0.X, "rank": %d, "status": "exploring"}

Output ONLY the JSON object.`,
		approach.Title, approach.Description, approach.Tradeoffs,
		approach.Confidence*100, feedback, codeContext, approach.Rank)

	// Deeper research is also read-only — use RunCLI (--print).
	result, err := pa.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("deeper research CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return &approach, nil
	}

	var updated ResearchedApproach
	if err := json.Unmarshal([]byte(jsonStr), &updated); err != nil {
		logger.Warn("Deeper research returned invalid JSON, keeping original approach", "error", err)
		return &approach, nil
	}
	updated.ID = approach.ID
	if updated.Status == "" {
		updated.Status = "exploring"
	}
	return &updated, nil
}

// AnswerQuestionActivity answers a user question about the approaches.
func (pa *PlanningActivities) AnswerQuestionActivity(ctx context.Context, req PlanningRequest, goal ClarifiedGoal, approaches []ResearchedApproach, question string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Answering question", "Question", question)

	approachJSON, err := json.Marshal(approaches)
	if err != nil {
		return "", fmt.Errorf("marshal approaches for answer: %w", err)
	}
	codeContext := pa.buildCodebaseContextForTask(ctx, req.WorkDir, goal.Intent+" "+question)
	prompt := fmt.Sprintf(`You are answering a planning question.

GOAL: %s
APPROACHES: %s
CODEBASE:
%s

QUESTION: %s

Answer the question thoroughly, referencing specific code and approaches where relevant.
Be concise but complete. No JSON needed — just a clear text answer.`, goal.Intent, string(approachJSON), codeContext, question)

	result, err := pa.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return "", fmt.Errorf("answer question CLI: %w", err)
	}
	return strings.TrimSpace(result.Output), nil
}

// DecomposeApproachActivity breaks a selected approach into implementation subtasks.
func (pa *PlanningActivities) DecomposeApproachActivity(ctx context.Context, req PlanningRequest, approach ResearchedApproach) ([]types.DecompStep, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Decomposing approach", "Title", approach.Title)

	codeContext := pa.buildCodebaseContextForTask(ctx, req.WorkDir, approach.Title+" "+approach.Description)
	prompt := fmt.Sprintf(`You are a senior software architect. Break this selected approach into implementation subtasks.

APPROACH: %s
DESCRIPTION: %s

CODEBASE:
%s

OUTPUT CONTRACT (strict JSON):
{"steps": [{"title": "...", "description": "...", "acceptance": "...", "estimate_minutes": N}]}

Rules:
- Each step must be independently implementable and testable
- Steps should be ordered by dependency (first step has no deps)
- Maximum 5 steps
- Each step needs clear acceptance criteria
- Output ONLY the JSON object.`, approach.Title, approach.Description, codeContext)

	result, err := pa.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("decompose CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("decomposition produced no JSON")
	}

	var decomp struct {
		Steps []types.DecompStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &decomp); err != nil {
		return nil, fmt.Errorf("parse decomposition: %w", err)
	}

	return decomp.Steps, nil
}

// BuildPlanSpecActivity turns the selected approach and decomposition into a binding plan artifact.
func (pa *PlanningActivities) BuildPlanSpecActivity(ctx context.Context, req PlanningRequest, goal ClarifiedGoal, approach ResearchedApproach, steps []types.DecompStep) (*types.PlanSpec, error) {
	codeContext := pa.buildCodebaseContextForTask(ctx, req.WorkDir, goal.Intent)
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return nil, fmt.Errorf("marshal steps: %w", err)
	}

	prompt := fmt.Sprintf(`You are converting an approved planning direction into an execution contract.

GOAL:
Intent: %s
Why: %s
Constraints: %s
Raw: %s

SELECTED APPROACH:
Title: %s
Description: %s
Tradeoffs: %s
Confidence: %.2f

DECOMPOSED STEPS:
%s

CODEBASE:
%s

OUTPUT CONTRACT (strict JSON):
{
  "problem_statement": "clear statement of the problem being solved",
  "desired_outcome": "what success looks like when this lands",
  "expected_pr_outcome": "what the PR should contain and prove",
  "summary": "one short paragraph tying the plan together",
  "assumptions": ["assumption 1"],
  "non_goals": ["explicitly out of scope item"],
  "risks": ["meaningful risk"],
  "validation_strategy": ["specific test or verification step"],
  "steps": [{"title":"...", "description":"...", "acceptance":"...", "estimate_minutes":10}]
}

Rules:
- Reuse the decomposed steps exactly unless a step is invalid; do not invent a different execution plan.
- Non-goals and risks must be concrete, not generic filler.
- Validation strategy must describe how the change will be tested or verified.
- Expected PR outcome must describe the code/test delta a reviewer should expect.
- Output ONLY the JSON object. No commentary.`, goal.Intent, goal.Why, strings.Join(goal.Constraints, ", "), goal.Raw, approach.Title, approach.Description, approach.Tradeoffs, approach.Confidence, string(stepsJSON), codeContext)

	result, err := pa.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("build plan spec CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("plan spec produced no JSON")
	}

	var spec types.PlanSpec
	if err := json.Unmarshal([]byte(jsonStr), &spec); err != nil {
		return nil, fmt.Errorf("parse plan spec: %w", err)
	}
	spec.Goal = toPlanningGoal(goal)
	spec.ChosenApproach = toPlanningApproach(approach)
	if len(spec.Steps) == 0 {
		spec.Steps = append(spec.Steps, steps...)
	}

	if err := validatePlanSpec(spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// CreatePlanSubtasksActivity creates DAG tasks from planning decomposition steps.
func (pa *PlanningActivities) CreatePlanSubtasksActivity(ctx context.Context, req PlanningRequest, steps []types.DecompStep) ([]string, error) {
	logger := activity.GetLogger(ctx)
	if err := validateDecompSteps(steps); err != nil {
		return nil, err
	}

	requiresBeads := pa.Config != nil &&
		pa.Config.BeadsBridge.Enabled &&
		planningIngressRequiresBeads(pa.Config.BeadsBridge.IngressPolicy)
	if !requiresBeads {
		var tasks []dag.Task
		for _, step := range steps {
			tasks = append(tasks, dag.Task{
				Title:           step.Title,
				Description:     step.Description,
				ParentID:        req.GoalID,
				Acceptance:      step.Acceptance,
				EstimateMinutes: step.Estimate,
				Project:         req.Project,
				Status:          string(types.StatusReady),
			})
		}

		ids, err := pa.DAG.CreateSubtasksAtomic(ctx, req.GoalID, tasks)
		if err != nil {
			return nil, fmt.Errorf("create plan subtasks: %w", err)
		}
		logger.Info("Created plan subtasks", "Parent", req.GoalID, "Count", len(ids))
		return ids, nil
	}

	bc := pa.beadsClient(req.Project)
	if bc == nil {
		return nil, fmt.Errorf("beads-first policy: no beads client configured for project %q", req.Project)
	}
	if _, isNull := bc.(*beads.NullStore); isNull {
		return nil, fmt.Errorf("beads-first policy: project %q is using NullStore (bd unavailable)", req.Project)
	}

	parentIssueID := strings.TrimSpace(req.GoalID)
	bridgeDAG, canMap := pa.DAG.(*dag.DAG)
	if canMap {
		if mapping, err := bridgeDAG.GetBeadsMappingByTask(ctx, req.Project, req.GoalID); err == nil && strings.TrimSpace(mapping.IssueID) != "" {
			parentIssueID = strings.TrimSpace(mapping.IssueID)
		}
	}

	type preparedSubtask struct {
		issueID string
		step    types.DecompStep
	}
	prepared := make([]preparedSubtask, 0, len(steps))
	rollbackPrepared := func(reason string) {
		for _, subtask := range prepared {
			issueID := strings.TrimSpace(subtask.issueID)
			if issueID == "" {
				continue
			}
			closeErr := bc.Close(ctx, issueID, reason)
			if closeErr != nil {
				logger.Warn("Failed to roll back beads subtask", "issueID", issueID, "error", closeErr)
			}
		}
	}
	var prevIssueID string
	for _, step := range steps {
		issueID, err := bc.Create(ctx, beads.CreateParams{
			Title:            step.Title,
			Description:      step.Description,
			IssueType:        "task",
			Priority:         -1,
			ParentID:         parentIssueID,
			Acceptance:       step.Acceptance,
			EstimatedMinutes: step.Estimate,
			Dependencies: func() []string {
				if prevIssueID == "" {
					return nil
				}
				return []string{prevIssueID}
			}(),
		})
		if err != nil {
			rollbackPrepared("Automatic rollback: beads subtask batch creation failed")
			return nil, fmt.Errorf("create beads subtask for %q: %w", step.Title, err)
		}
		issueID = strings.TrimSpace(issueID)
		if issueID == "" {
			rollbackPrepared("Automatic rollback: beads returned empty subtask id")
			return nil, fmt.Errorf("create beads subtask for %q: empty issue id", step.Title)
		}
		prepared = append(prepared, preparedSubtask{
			issueID: issueID,
			step:    step,
		})
		prevIssueID = issueID
	}

	var tasks []dag.Task
	for _, subtask := range prepared {
		tasks = append(tasks, dag.Task{
			ID:              subtask.issueID,
			Title:           subtask.step.Title,
			Description:     subtask.step.Description,
			ParentID:        req.GoalID,
			Acceptance:      subtask.step.Acceptance,
			EstimateMinutes: subtask.step.Estimate,
			Project:         req.Project,
			Status:          string(types.StatusReady),
			Metadata: map[string]string{
				"beads_issue_id": subtask.issueID,
				"beads_bridge":   "true",
			},
		})
	}

	ids, err := pa.DAG.CreateSubtasksAtomic(ctx, req.GoalID, tasks)
	if err != nil {
		rollbackPrepared("Automatic rollback: DAG subtask creation failed")
		return nil, fmt.Errorf("create plan subtasks: %w", err)
	}
	if canMap {
		for _, subtask := range prepared {
			fingerprint := ""
			if issue, showErr := bc.Show(ctx, subtask.issueID); showErr == nil {
				fingerprint = beadsbridge.FingerprintIssue(issue)
			}
			if err := bridgeDAG.UpsertBeadsMapping(ctx, req.Project, subtask.issueID, subtask.issueID, fingerprint); err != nil {
				// DAG subtasks are already committed here; keep execution moving and
				// rely on dispatch backfill (taskID==issueID) to repair mapping later.
				logger.Warn("Persist beads mapping failed after planning subtask commit",
					"project", req.Project,
					"taskID", subtask.issueID,
					"issueID", subtask.issueID,
					"error", err,
				)
			}
		}
	}
	logger.Info("Created plan subtasks", "Parent", req.GoalID, "Count", len(ids))
	return ids, nil
}

// RecordPlanningDecisionActivity creates a decision node in the DAG linking
// the selected approach to all considered alternatives. This feeds the decision
// blockchain so future planning ceremonies can learn from past choices.
func (pa *PlanningActivities) RecordPlanningDecisionActivity(ctx context.Context, req PlanningRequest, selected ResearchedApproach, all []ResearchedApproach) (string, error) {
	logger := activity.GetLogger(ctx)

	if pa.Decisions == nil {
		logger.Warn("No decision store configured, skipping decision recording")
		return "", nil
	}

	dec := dag.Decision{
		TaskID:  req.GoalID,
		Title:   fmt.Sprintf("Planning: approach selection for %s", req.GoalID),
		Context: fmt.Sprintf("Planning session %s evaluated %d approaches", req.SessionID, len(all)),
		Outcome: selected.Title,
	}

	decID, err := pa.Decisions.CreateDecision(ctx, dec)
	if err != nil {
		return "", fmt.Errorf("create planning decision: %w", err)
	}

	for _, a := range all {
		alt := dag.Alternative{
			DecisionID: decID,
			Label:      a.Title,
			Reasoning:  fmt.Sprintf("%s\nTradeoffs: %s", a.Description, a.Tradeoffs),
			Selected:   a.ID == selected.ID,
			UCTScore:   a.Confidence,
			Visits:     1,
			Reward:     a.Confidence,
		}
		if _, err := pa.Decisions.CreateAlternative(ctx, alt); err != nil {
			logger.Warn("Failed to create alternative", "title", a.Title, "error", err)
		}
	}

	logger.Info("Recorded planning decision",
		"DecisionID", decID,
		"Selected", selected.Title,
		"Alternatives", len(all))

	return decID, nil
}

// NotifyChatActivity sends a message to a Matrix room.
func (pa *PlanningActivities) NotifyChatActivity(ctx context.Context, roomID, message string) error {
	if pa.ChatSend == nil {
		return nil
	}
	return pa.ChatSend.Send(ctx, roomID, message)
}

// --- helpers ---

func (pa *PlanningActivities) beadsClient(project string) beads.Store {
	if pa.BeadsClients == nil {
		return nil
	}
	return pa.BeadsClients[project]
}

func planningIngressRequiresBeads(policy string) bool {
	p := strings.ToLower(strings.TrimSpace(policy))
	return p != "" && p != "legacy"
}

func toPlanningGoal(goal ClarifiedGoal) types.PlanningGoal {
	return types.PlanningGoal{
		Intent:      goal.Intent,
		Constraints: append([]string{}, goal.Constraints...),
		Why:         goal.Why,
		Raw:         goal.Raw,
	}
}

func toPlanningApproach(approach ResearchedApproach) types.PlanningApproach {
	return types.PlanningApproach{
		ID:          approach.ID,
		Title:       approach.Title,
		Description: approach.Description,
		Tradeoffs:   approach.Tradeoffs,
		Confidence:  approach.Confidence,
		Rank:        approach.Rank,
		Status:      approach.Status,
	}
}

func validatePlanSpec(spec types.PlanSpec) error {
	switch {
	case strings.TrimSpace(spec.ProblemStatement) == "":
		return fmt.Errorf("plan spec invalid: missing problem statement")
	case strings.TrimSpace(spec.DesiredOutcome) == "":
		return fmt.Errorf("plan spec invalid: missing desired outcome")
	case strings.TrimSpace(spec.ExpectedPROutcome) == "":
		return fmt.Errorf("plan spec invalid: missing expected PR outcome")
	case strings.TrimSpace(spec.Summary) == "":
		return fmt.Errorf("plan spec invalid: missing summary")
	case strings.TrimSpace(spec.ChosenApproach.Title) == "":
		return fmt.Errorf("plan spec invalid: missing chosen approach")
	case len(spec.NonGoals) == 0:
		return fmt.Errorf("plan spec invalid: at least one non-goal is required")
	case len(spec.Risks) == 0:
		return fmt.Errorf("plan spec invalid: at least one risk is required")
	case len(spec.ValidationStrategy) == 0:
		return fmt.Errorf("plan spec invalid: validation strategy is required")
	case len(spec.Steps) == 0:
		return fmt.Errorf("plan spec invalid: at least one execution step is required")
	}

	for i, step := range spec.Steps {
		if err := validateDecompStep(step, i+1, "plan spec invalid"); err != nil {
			return err
		}
	}
	return nil
}

func validateDecompSteps(steps []types.DecompStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("plan spec invalid: at least one execution step is required")
	}
	for i, step := range steps {
		if err := validateDecompStep(step, i+1, "subtask creation blocked"); err != nil {
			return err
		}
	}
	return nil
}

func validateDecompStep(step types.DecompStep, index int, prefix string) error {
	switch {
	case strings.TrimSpace(step.Title) == "":
		return fmt.Errorf("%s: step %d missing title", prefix, index)
	case strings.TrimSpace(step.Description) == "":
		return fmt.Errorf("%s: step %d missing description", prefix, index)
	case strings.TrimSpace(step.Acceptance) == "":
		return fmt.Errorf("%s: step %d missing acceptance", prefix, index)
	case step.Estimate <= 0:
		return fmt.Errorf("%s: step %d missing estimate", prefix, index)
	case step.Estimate > 15:
		return fmt.Errorf("%s: step %d exceeds 15 minute cap", prefix, index)
	}
	return nil
}

func (pa *PlanningActivities) buildCodebaseContext(ctx context.Context, workDir string) string {
	return pa.buildCodebaseContextForTask(ctx, workDir, "")
}

// buildCodebaseContextForTask produces AST-based codebase context filtered by
// relevance to the given task prompt. Relevant files get full source detail,
// the rest get signatures only.
func (pa *PlanningActivities) buildCodebaseContextForTask(ctx context.Context, workDir, taskPrompt string) string {
	if pa.AST != nil {
		files, err := pa.AST.ParseDir(ctx, workDir)
		if err == nil && len(files) > 0 {
			if taskPrompt != "" {
				ef := astpkg.NewEmbedFilter()
				relevant, surrounding := ef.FilterRelevantByEmbedding(ctx, taskPrompt, files)
				if len(relevant) > 0 {
					return astpkg.SummarizeTargeted(surrounding, relevant)
				}
			}
			return astpkg.Summarize(files)
		}
	}
	return "(codebase context unavailable)"
}

// parseApproaches unmarshals LLM output that may be a JSON array directly
// or wrapped in an object (e.g. {"approaches": [...]}).
func parseApproaches(jsonStr string) ([]ResearchedApproach, error) {
	data := []byte(jsonStr)

	// Try direct array first.
	var approaches []ResearchedApproach
	if err := json.Unmarshal(data, &approaches); err == nil && len(approaches) > 0 {
		return approaches, nil
	}

	// Try object wrapper — LLMs commonly wrap arrays in {"approaches": [...]} or similar.
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("neither array nor object: %w", err)
	}

	// Look for any key whose value is an array of approaches.
	for _, v := range wrapper {
		var arr []ResearchedApproach
		if err := json.Unmarshal(v, &arr); err == nil && len(arr) > 0 {
			return arr, nil
		}
	}

	// Last resort: try to unmarshal each value as a single approach
	// (some LLMs return {"1": {...}, "2": {...}} keyed by rank).
	// Sort keys to preserve LLM's intended ordering.
	keys := make([]string, 0, len(wrapper))
	for k := range wrapper {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var singles []ResearchedApproach
	for _, k := range keys {
		var single ResearchedApproach
		if err := json.Unmarshal(wrapper[k], &single); err == nil && single.Title != "" {
			singles = append(singles, single)
		}
	}
	if len(singles) > 0 {
		return singles, nil
	}

	return nil, fmt.Errorf("no approach array found in object keys")
}

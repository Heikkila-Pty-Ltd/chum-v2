package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"go.temporal.io/sdk/activity"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
)

// ChatSendFunc sends a message to a Matrix room. Injected to avoid import cycles.
type ChatSendFunc func(ctx context.Context, roomID, message string) error

// PlanningActivities holds dependencies for planning Temporal activities.
type PlanningActivities struct {
	DAG          *dag.DAG
	Config       *config.Config
	Logger       *slog.Logger
	AST          *astpkg.Parser
	BeadsClients map[string]*beads.Client
	ChatSend     ChatSendFunc
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

	codeContext := pa.buildCodebaseContext(ctx, req.WorkDir)
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

	result, err := llm.RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
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

	codeContext := pa.buildCodebaseContext(ctx, req.WorkDir)
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
	result, err := llm.RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("research CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("research produced no JSON output")
	}

	var approaches []ResearchedApproach
	if err := json.Unmarshal([]byte(jsonStr), &approaches); err != nil {
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

	approachJSON, _ := json.Marshal(approaches)
	prompt := fmt.Sprintf(`You are reviewing research approaches for goal alignment.

ORIGINAL GOAL: %s
WHY: %s

APPROACHES:
%s

For each approach, verify it actually serves the goal. If an approach has drifted from the intent, adjust its confidence downward. If an approach is well-aligned, confirm or slightly adjust confidence.

OUTPUT CONTRACT (strict JSON array — same structure as input, with potentially adjusted confidence values):
[{"title": "...", "description": "...", "tradeoffs": "...", "confidence": 0.X, "rank": N, "status": "exploring"}]

Output ONLY the JSON array.`, goal.Intent, goal.Why, string(approachJSON))

	result, err := llm.RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
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

	codeContext := pa.buildCodebaseContext(ctx, req.WorkDir)
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
	result, err := llm.RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
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

	approachJSON, _ := json.Marshal(approaches)
	codeContext := pa.buildCodebaseContext(ctx, req.WorkDir)
	prompt := fmt.Sprintf(`You are answering a planning question.

GOAL: %s
APPROACHES: %s
CODEBASE:
%s

QUESTION: %s

Answer the question thoroughly, referencing specific code and approaches where relevant.
Be concise but complete. No JSON needed — just a clear text answer.`, goal.Intent, string(approachJSON), codeContext, question)

	result, err := llm.RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return "", fmt.Errorf("answer question CLI: %w", err)
	}
	return strings.TrimSpace(result.Output), nil
}

// DecomposeApproachActivity breaks a selected approach into implementation subtasks.
func (pa *PlanningActivities) DecomposeApproachActivity(ctx context.Context, req PlanningRequest, approach ResearchedApproach) ([]DecompStep, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Decomposing approach", "Title", approach.Title)

	codeContext := pa.buildCodebaseContext(ctx, req.WorkDir)
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

	result, err := llm.RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("decompose CLI: %w", err)
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("decomposition produced no JSON")
	}

	var decomp struct {
		Steps []DecompStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &decomp); err != nil {
		return nil, fmt.Errorf("parse decomposition: %w", err)
	}

	return decomp.Steps, nil
}

// CreatePlanSubtasksActivity creates DAG tasks from planning decomposition steps.
func (pa *PlanningActivities) CreatePlanSubtasksActivity(ctx context.Context, req PlanningRequest, steps []DecompStep) ([]string, error) {
	logger := activity.GetLogger(ctx)

	var tasks []dag.Task
	for _, step := range steps {
		tasks = append(tasks, dag.Task{
			Title:           step.Title,
			Description:     step.Description,
			ParentID:        req.GoalID,
			Acceptance:      step.Acceptance,
			EstimateMinutes: step.Estimate,
			Project:         req.Project,
			Status:          "ready",
		})
	}

	ids, err := pa.DAG.CreateSubtasksAtomic(ctx, req.GoalID, tasks)
	if err != nil {
		return nil, fmt.Errorf("create plan subtasks: %w", err)
	}
	logger.Info("Created plan subtasks", "Parent", req.GoalID, "Count", len(ids))
	return ids, nil
}

// NotifyChatActivity sends a message to a Matrix room.
func (pa *PlanningActivities) NotifyChatActivity(ctx context.Context, roomID, message string) error {
	if pa.ChatSend == nil {
		return nil
	}
	return pa.ChatSend(ctx, roomID, message)
}

// --- helpers ---

func (pa *PlanningActivities) beadsClient(project string) *beads.Client {
	if pa.BeadsClients == nil {
		return nil
	}
	return pa.BeadsClients[project]
}

func (pa *PlanningActivities) buildCodebaseContext(ctx context.Context, workDir string) string {
	if pa.AST != nil {
		files, err := pa.AST.ParseDir(ctx, workDir)
		if err == nil && len(files) > 0 {
			return astpkg.Summarize(files)
		}
	}
	return "(codebase context unavailable)"
}

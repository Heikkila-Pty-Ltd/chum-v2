package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/sdk/activity"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// DecomposeActivity runs the LLM in plan mode to break a task into sub-steps.
// Returns Atomic=true (empty Steps) if the task is already concrete enough.
func (a *Activities) DecomposeActivity(ctx context.Context, req TaskRequest) (*DecompResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Decomposing task", "TaskID", req.TaskID)

	codeContext := a.buildCodebaseContext(ctx, req.WorkDir)
	prompt := buildDecompPrompt(req.Prompt, codeContext)

	result, err := RunCLI(req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("decompose CLI: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("decompose CLI exited %d: %s", result.ExitCode, truncate(result.Output, 500))
	}

	jsonStr := ExtractJSON(result.Output)
	if jsonStr == "" {
		logger.Warn("Decomposition produced no JSON, treating as atomic")
		return &DecompResult{Atomic: true}, nil
	}

	var decomp DecompResult
	if err := json.Unmarshal([]byte(jsonStr), &decomp); err != nil {
		logger.Warn("Failed to parse decomposition JSON, treating as atomic", "error", err)
		return &DecompResult{Atomic: true}, nil
	}

	if len(decomp.Steps) == 0 {
		decomp.Atomic = true
	}

	logger.Info("Decomposition result", "Steps", len(decomp.Steps), "Atomic", decomp.Atomic)
	return &decomp, nil
}

// CreateSubtasksActivity creates DAG tasks from decomposition steps,
// wires sequential dependencies, and marks the parent as "decomposed".
func (a *Activities) CreateSubtasksActivity(ctx context.Context, parentID, project string, steps []DecompStep) ([]string, error) {
	logger := activity.GetLogger(ctx)
	var ids []string

	for _, step := range steps {
		task := dag.Task{
			Title:           step.Title,
			Description:     step.Description,
			Status:          "open",
			ParentID:        parentID,
			Acceptance:      step.Acceptance,
			EstimateMinutes: step.Estimate,
			Project:         project,
		}
		id, err := a.DAG.CreateTask(ctx, task)
		if err != nil {
			return nil, fmt.Errorf("create subtask %q: %w", step.Title, err)
		}
		ids = append(ids, id)
		logger.Info("Created subtask", "ID", id, "Title", step.Title, "Parent", parentID)
	}

	// Wire sequential dependencies: step[i+1] depends on step[i]
	for i := 1; i < len(ids); i++ {
		if err := a.DAG.AddEdge(ctx, ids[i], ids[i-1]); err != nil {
			logger.Warn("Failed to add subtask edge", "from", ids[i], "to", ids[i-1], "error", err)
		}
	}

	// Rewire: any task that depended on the parent now depends on the last subtask.
	// Without this, dependents would block forever since "decomposed" != "completed".
	lastSubtask := ids[len(ids)-1]
	dependents, err := a.DAG.GetDependents(ctx, parentID)
	if err != nil {
		logger.Warn("Failed to get parent dependents for rewiring", "parentID", parentID, "error", err)
	} else {
		for _, dep := range dependents {
			if err := a.DAG.AddEdge(ctx, dep, lastSubtask); err != nil {
				logger.Warn("Failed to add rewired edge", "from", dep, "to", lastSubtask, "error", err)
			}
			if err := a.DAG.RemoveEdge(ctx, dep, parentID); err != nil {
				logger.Warn("Failed to remove old edge", "from", dep, "to", parentID, "error", err)
			}
			logger.Info("Rewired dependency", "dependent", dep, "oldDep", parentID, "newDep", lastSubtask)
		}
	}

	// Mark parent as decomposed
	if err := a.DAG.UpdateTaskStatus(ctx, parentID, "decomposed"); err != nil {
		logger.Warn("Failed to mark parent as decomposed", "parentID", parentID, "error", err)
	}

	return ids, nil
}

func buildDecompPrompt(taskPrompt, codeContext string) string {
	return fmt.Sprintf(`You are a senior software architect. Analyze the following task and decide whether it should be broken into smaller sub-tasks.

TASK:
%s

CODEBASE:
%s

OUTPUT CONTRACT (strict):
Return a JSON object with this schema:
{"steps": [{"title": "...", "description": "...", "acceptance": "...", "estimate_minutes": N}]}

Rules:
- Each step must be independently implementable and testable.
- Each step must have a clear title, detailed description, acceptance criteria, and time estimate.
- If the task is already atomic (a single clear, bounded change), return {"steps": []} to indicate no decomposition needed.
- Do NOT decompose tasks that are already specific and bounded (e.g. "add field X to struct Y", "fix bug in function Z").
- DO decompose tasks that are vague, multi-part, or touch multiple subsystems.
- Maximum 5 steps. Prefer fewer, larger steps over many tiny ones.
- Output ONLY the JSON object. No commentary, no markdown fences.`, taskPrompt, codeContext)
}

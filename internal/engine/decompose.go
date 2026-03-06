package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/admit"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beadsbridge"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// DecomposeActivity runs the LLM in plan mode to break a task into sub-steps.
// Returns Atomic=true (empty Steps) if the task is already concrete enough.
func (a *Activities) DecomposeActivity(ctx context.Context, req TaskRequest) (*types.DecompResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Decomposing task", "TaskID", req.TaskID)

	codeContext := a.buildCodebaseContextForTask(ctx, req.WorkDir, req.Prompt)
	prompt := buildDecompPrompt(req.Prompt, codeContext)

	result, err := a.LLM.Plan(ctx, req.Agent, req.Model, req.WorkDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("decompose CLI: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("decompose CLI exited %d: %s", result.ExitCode, types.Truncate(result.Output, 500))
	}

	jsonStr := llm.ExtractJSON(result.Output)
	if jsonStr == "" {
		logger.Warn("Decomposition produced no JSON, treating as atomic")
		return &types.DecompResult{Atomic: true}, nil
	}

	decomp, err := parseDecompJSON(jsonStr)
	if err != nil {
		logger.Warn("Failed to parse decomposition JSON, treating as atomic", "error", err)
		return &types.DecompResult{Atomic: true}, nil
	}

	logger.Info("Decomposition result", "Steps", len(decomp.Steps), "Atomic", decomp.Atomic)
	return &decomp, nil
}

// CreateSubtasksActivity creates DAG tasks from decomposition steps,
// wires sequential dependencies, rewires parent dependents to the last
// subtask, and marks the parent as "decomposed" — all atomically.
// If any step fails, the entire operation is rolled back (no partial children).
func (a *Activities) CreateSubtasksActivity(ctx context.Context, parentID, project string, steps []types.DecompStep) ([]string, error) {
	logger := activity.GetLogger(ctx)

	bc, ok := a.BeadsClients[project]
	if !ok || bc == nil {
		return nil, fmt.Errorf("beads-first policy: no beads client configured for project %q", project)
	}

	parentIssueID := strings.TrimSpace(parentID)
	bridgeDAG, canMap := a.DAG.(*dag.DAG)
	if canMap {
		if mapping, err := bridgeDAG.GetBeadsMappingByTask(ctx, project, parentID); err == nil && strings.TrimSpace(mapping.IssueID) != "" {
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
			Priority:         -1, // Use beads default priority unless planner explicitly changes it later.
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
			ParentID:        parentID,
			Acceptance:      subtask.step.Acceptance,
			EstimateMinutes: subtask.step.Estimate,
			Project:         project,
			Metadata: map[string]string{
				"beads_issue_id": subtask.issueID,
				"beads_bridge":   "true",
			},
		})
	}

	ids, err := a.DAG.CreateSubtasksAtomic(ctx, parentID, tasks)
	if err != nil {
		rollbackPrepared("Automatic rollback: DAG subtask creation failed")
		return nil, fmt.Errorf("create subtasks for %s: %w", parentID, err)
	}
	if canMap {
		for _, subtask := range prepared {
			fingerprint := ""
			if issue, showErr := bc.Show(ctx, subtask.issueID); showErr == nil {
				fingerprint = beadsbridge.FingerprintIssue(issue)
			}
			if err := bridgeDAG.UpsertBeadsMapping(ctx, project, subtask.issueID, subtask.issueID, fingerprint); err != nil {
				// DAG subtasks are already committed here; keep execution moving and
				// rely on dispatch backfill (taskID==issueID) to repair mapping later.
				logger.Warn("Persist beads mapping failed after subtask commit",
					"project", project,
					"taskID", subtask.issueID,
					"issueID", subtask.issueID,
					"error", err,
				)
			}
		}
	}
	logger.Info("Created subtasks atomically", "Parent", parentID, "Count", len(ids), "IDs", ids)

	// Run admission gate to validate, resolve targets, and promote open → ready.
	// Subtasks must pass the same structural checks and conflict fence logic as
	// any other task. Without this they'd sit in "open" until the next chum sync.
	proj, ok := a.Config.Projects[project]
	if ok && a.AST != nil {
		gateResult, err := admit.RunGate(ctx, a.DAG, a.AST, project, proj.Workspace, a.Logger)
		if err != nil {
			logger.Warn("Admission gate failed after subtask creation", "error", err)
		} else {
			logger.Info("Admission gate ran inline", "result", gateResult.String())
		}
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
- Maximum estimate per step: 15 minutes. Prefer 5-10 minute steps.
- Each step should require at most 15 tool calls (file reads, edits, test runs).
- Each step should touch at most 3 files.
- If the task is already atomic (a single clear, bounded change completable in ≤15 minutes), return {"steps": []} to indicate no decomposition needed.
- Do NOT decompose tasks that are already specific and bounded (e.g. "add field X to struct Y", "fix bug in function Z").
- DO decompose tasks that are vague, multi-part, or touch multiple subsystems.
- Maximum 5 steps. Prefer fewer, larger steps over many tiny ones.
- ANTI-PATTERNS (never create steps like these):
  - "Refactor X and verify all tests pass" → split into "Refactor X" and "Run test suite"
  - "Add feature and update docs and tests" → three separate steps
  - "Investigate and fix" → split into "Diagnose root cause" and "Apply fix"
- Output ONLY the JSON object. No commentary, no markdown fences.`, taskPrompt, codeContext)
}

// parseDecompJSON accepts both canonical object form:
// {"steps":[...]} and bare-array form: [{...}, {...}].
func parseDecompJSON(jsonStr string) (types.DecompResult, error) {
	var decomp types.DecompResult
	if err := json.Unmarshal([]byte(jsonStr), &decomp); err == nil {
		if len(decomp.Steps) == 0 {
			decomp.Atomic = true
		}
		return decomp, nil
	}

	var steps []types.DecompStep
	if err := json.Unmarshal([]byte(jsonStr), &steps); err == nil {
		return types.DecompResult{
			Steps:  steps,
			Atomic: len(steps) == 0,
		}, nil
	}

	return types.DecompResult{}, fmt.Errorf("unsupported decomposition JSON shape")
}

package admit

import (
	"context"
	"fmt"
	"log/slog"

	astpkg "github.com/Heikkila-Pty-Ltd/chum-v2/internal/ast"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

// CodebaseParser abstracts AST parsing for testability.
type CodebaseParser interface {
	ParseDir(ctx context.Context, dir string) ([]*astpkg.ParsedFile, error)
}

// GateResult summarizes what the admission gate did during this sync.
type GateResult struct {
	Promoted        int
	NeedsRefinement int
	MarkedStale     int
	FencesAdded     int
	Errors          []string
}

func (r GateResult) String() string {
	return fmt.Sprintf("promoted=%d needs_refinement=%d stale=%d fences=%d errors=%d",
		r.Promoted, r.NeedsRefinement, r.MarkedStale, r.FencesAdded, len(r.Errors))
}

// RunGate is the admission gate entry point. It validates open tasks,
// resolves their code targets, checks for staleness, and computes
// conflict fences.
func RunGate(ctx context.Context, d dag.TaskStore, parser CodebaseParser, project, workspace string, logger *slog.Logger) (GateResult, error) {
	var result GateResult

	// Step 0: Parse codebase to build symbol index
	files, err := parser.ParseDir(ctx, workspace)
	if err != nil {
		return result, fmt.Errorf("parse codebase: %w", err)
	}
	index := BuildSymbolIndex(files)
	logger.Info("Admission gate: codebase parsed", "files", len(files), "symbols", countSymbols(index))

	// Step 1: Clean old AST fence edges
	if err := d.DeleteEdgesBySource(ctx, project, "ast"); err != nil {
		return result, fmt.Errorf("clean ast edges: %w", err)
	}

	// Step 2: Process 'open' tasks → validate and promote
	openTasks, err := d.ListTasks(ctx, project, string(types.StatusOpen))
	if err != nil {
		return result, fmt.Errorf("list open tasks: %w", err)
	}
	for _, task := range openTasks {
		vr := ValidateStructure(task)
		if !vr.Pass {
			if err := d.UpdateTask(ctx, task.ID, map[string]any{
				"status":    types.StatusNeedsRefinement,
				"error_log": vr.Reason,
			}); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("mark %s needs_refinement: %v", task.ID, err))
				continue
			}
			logger.Info("Task needs refinement", "TaskID", task.ID, "Reason", vr.Reason)
			result.NeedsRefinement++
			continue
		}

		// Resolve targets
		targets := ResolveTargets(task, index)
		if err := d.SetTaskTargets(ctx, task.ID, targets); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("set targets %s: %v", task.ID, err))
			continue
		}

		// Promote to ready
		if err := d.UpdateTask(ctx, task.ID, map[string]any{"status": types.StatusReady}); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("promote %s: %v", task.ID, err))
			continue
		}
		logger.Info("Task promoted to ready", "TaskID", task.ID, "Targets", len(targets))
		result.Promoted++
	}

	// Step 3: Re-check ready tasks for staleness
	readyTasks, err := d.ListTasks(ctx, project, string(types.StatusReady))
	if err != nil {
		return result, fmt.Errorf("list ready tasks: %w", err)
	}
	for _, task := range readyTasks {
		oldTargets, err := d.GetTaskTargets(ctx, task.ID)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("get targets %s: %v", task.ID, err))
			continue
		}
		if len(oldTargets) == 0 {
			continue // no targets to go stale
		}

		// Re-resolve and update stored targets
		newTargets := ResolveTargets(task, index)
		if err := d.SetTaskTargets(ctx, task.ID, newTargets); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("update targets %s: %v", task.ID, err))
			continue
		}

		if CheckStaleness(oldTargets, index) {
			if err := d.UpdateTask(ctx, task.ID, map[string]any{
				"status":    types.StatusStale,
				"error_log": "referenced code has changed since task was promoted",
			}); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("mark %s stale: %v", task.ID, err))
				continue
			}
			logger.Warn("Task marked stale", "TaskID", task.ID)
			result.MarkedStale++
		}
	}

	// Step 4: Compute conflict fences for ready + running tasks
	allActive, err := d.ListTasks(ctx, project, string(types.StatusReady), string(types.StatusRunning))
	if err != nil {
		return result, fmt.Errorf("list active tasks: %w", err)
	}
	activeTargets, err := d.GetAllTargetsForStatuses(ctx, project, string(types.StatusReady), string(types.StatusRunning))
	if err != nil {
		return result, fmt.Errorf("get active targets: %w", err)
	}

	fences := ComputeFences(allActive, activeTargets)
	for _, fence := range fences {
		if err := d.AddEdgeWithSource(ctx, fence.From, fence.To, "ast"); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("add fence %s→%s: %v", fence.From, fence.To, err))
			continue
		}
		result.FencesAdded++
	}
	if result.FencesAdded > 0 {
		logger.Info("Conflict fences added", "count", result.FencesAdded)
	}

	return result, nil
}

func countSymbols(idx *SymbolIndex) int {
	n := 0
	for _, hits := range idx.byName {
		n += len(hits)
	}
	return n
}

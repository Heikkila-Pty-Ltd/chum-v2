package admit

import (
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

// FenceEdge represents a serialization dependency between two tasks
// that touch overlapping code.
type FenceEdge struct {
	From string // must wait (lower priority = higher number, or later ID)
	To   string // runs first (higher priority = lower number, or earlier ID)
}

// ComputeFences detects overlapping targets between tasks and returns
// edges that serialize conflicting work. The lower-priority task depends
// on the higher-priority one.
func ComputeFences(tasks []dag.Task, targetsByTask map[string][]dag.TaskTarget) []FenceEdge {
	// Build reverse index: (file, symbol) → [taskIDs]
	type targetKey struct {
		filePath   string
		symbolName string
	}
	overlap := make(map[targetKey][]string)
	for _, t := range tasks {
		for _, tgt := range targetsByTask[t.ID] {
			key := targetKey{tgt.FilePath, tgt.SymbolName}
			overlap[key] = append(overlap[key], t.ID)
		}
	}

	// Build priority lookup
	priority := make(map[string]int)
	for _, t := range tasks {
		priority[t.ID] = t.Priority
	}

	// For each overlapping key, create edges between all pairs
	seen := make(map[FenceEdge]bool)
	var edges []FenceEdge

	for _, taskIDs := range overlap {
		if len(taskIDs) < 2 {
			continue
		}
		for i := 0; i < len(taskIDs); i++ {
			for j := i + 1; j < len(taskIDs); j++ {
				a, b := taskIDs[i], taskIDs[j]
				from, to := orderByPriority(a, b, priority)
				edge := FenceEdge{From: from, To: to}
				if !seen[edge] {
					seen[edge] = true
					edges = append(edges, edge)
				}
			}
		}
	}

	return edges
}

// orderByPriority returns (waiter, runner) where the higher-priority task
// (lower number) runs first. Tiebreak: lexicographic task ID.
func orderByPriority(a, b string, priority map[string]int) (from, to string) {
	pa, pb := priority[a], priority[b]
	if pa < pb {
		// a has higher priority (lower number), runs first
		return b, a
	}
	if pb < pa {
		return a, b
	}
	// Same priority — lexicographic tiebreak
	if a < b {
		return b, a
	}
	return a, b
}

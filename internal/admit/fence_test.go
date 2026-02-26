package admit

import (
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

func TestComputeFences_Overlap(t *testing.T) {
	tasks := []dag.Task{
		{ID: "task-1", Priority: 0},
		{ID: "task-2", Priority: 1},
	}
	targets := map[string][]dag.TaskTarget{
		"task-1": {{FilePath: "parser.go", SymbolName: "Parser"}},
		"task-2": {{FilePath: "parser.go", SymbolName: "Parser"}},
	}

	edges := ComputeFences(tasks, targets)
	if len(edges) != 1 {
		t.Fatalf("expected 1 fence edge, got %d", len(edges))
	}
	// task-1 has higher priority (0), so task-2 waits
	if edges[0].From != "task-2" || edges[0].To != "task-1" {
		t.Errorf("expected task-2 → task-1, got %s → %s", edges[0].From, edges[0].To)
	}
}

func TestComputeFences_NoOverlap(t *testing.T) {
	tasks := []dag.Task{
		{ID: "task-1", Priority: 0},
		{ID: "task-2", Priority: 0},
	}
	targets := map[string][]dag.TaskTarget{
		"task-1": {{FilePath: "parser.go", SymbolName: "Parser"}},
		"task-2": {{FilePath: "dag.go", SymbolName: "DAG"}},
	}

	edges := ComputeFences(tasks, targets)
	if len(edges) != 0 {
		t.Errorf("expected 0 fence edges, got %d", len(edges))
	}
}

func TestComputeFences_SamePriorityTiebreak(t *testing.T) {
	tasks := []dag.Task{
		{ID: "task-a", Priority: 1},
		{ID: "task-b", Priority: 1},
	}
	targets := map[string][]dag.TaskTarget{
		"task-a": {{FilePath: "f.go", SymbolName: "Foo"}},
		"task-b": {{FilePath: "f.go", SymbolName: "Foo"}},
	}

	edges := ComputeFences(tasks, targets)
	if len(edges) != 1 {
		t.Fatalf("expected 1 fence edge, got %d", len(edges))
	}
	// task-a < task-b lexicographically, so task-a runs first
	if edges[0].From != "task-b" || edges[0].To != "task-a" {
		t.Errorf("expected task-b → task-a (lex tiebreak), got %s → %s", edges[0].From, edges[0].To)
	}
}

func TestComputeFences_ThreeTasks_PartialOverlap(t *testing.T) {
	tasks := []dag.Task{
		{ID: "task-1", Priority: 0},
		{ID: "task-2", Priority: 1},
		{ID: "task-3", Priority: 2},
	}
	targets := map[string][]dag.TaskTarget{
		"task-1": {{FilePath: "a.go", SymbolName: "Alpha"}},
		"task-2": {{FilePath: "a.go", SymbolName: "Alpha"}, {FilePath: "b.go", SymbolName: "Beta"}},
		"task-3": {{FilePath: "b.go", SymbolName: "Beta"}},
	}

	edges := ComputeFences(tasks, targets)
	// Expected: task-2 waits on task-1 (overlap on Alpha), task-3 waits on task-2 (overlap on Beta)
	edgeSet := make(map[FenceEdge]bool)
	for _, e := range edges {
		edgeSet[e] = true
	}

	if !edgeSet[FenceEdge{From: "task-2", To: "task-1"}] {
		t.Error("expected fence: task-2 → task-1")
	}
	if !edgeSet[FenceEdge{From: "task-3", To: "task-2"}] {
		t.Error("expected fence: task-3 → task-2")
	}
	// task-1 and task-3 don't overlap directly
	if edgeSet[FenceEdge{From: "task-3", To: "task-1"}] || edgeSet[FenceEdge{From: "task-1", To: "task-3"}] {
		t.Error("unexpected fence between task-1 and task-3")
	}
}

func TestComputeFences_FileLevel(t *testing.T) {
	tasks := []dag.Task{
		{ID: "task-1", Priority: 0},
		{ID: "task-2", Priority: 0},
	}
	targets := map[string][]dag.TaskTarget{
		"task-1": {{FilePath: "main.go"}}, // file-level, no symbol
		"task-2": {{FilePath: "main.go"}},
	}

	edges := ComputeFences(tasks, targets)
	if len(edges) != 1 {
		t.Fatalf("expected 1 fence edge for file-level overlap, got %d", len(edges))
	}
}

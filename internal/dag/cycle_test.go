package dag

import (
	"context"
	"errors"
	"testing"
)

// --- wouldCreateCycle ---

func TestWouldCreateCycle_NoCycle(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// A → B → C (linear chain, no cycle)
	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})
	d.AddEdge(ctx, "a", "b") // a depends on b
	d.AddEdge(ctx, "b", "c") // b depends on c

	// Adding c→a would NOT cycle (c depends on a, and a→b→c doesn't reach a from c's deps)
	// Wait — c has no deps. So adding c depends on some new thing is fine.
	// Let's check: would adding a new edge d→a cycle? No, d has no deps.
	d.CreateTask(ctx, Task{ID: "d", Project: "p"})
	wouldCycle, err := d.wouldCreateCycle(ctx, "d", "a")
	if err != nil {
		t.Fatal(err)
	}
	if wouldCycle {
		t.Error("expected no cycle for d→a")
	}
}

func TestWouldCreateCycle_DirectCycle(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})

	// a depends on b (via raw SQL to bypass cycle check)
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('a', 'b', 'test')")

	// Would b→a (b depends on a) create a cycle? Yes: b→a→b
	wouldCycle, err := d.wouldCreateCycle(ctx, "b", "a")
	if err != nil {
		t.Fatal(err)
	}
	if !wouldCycle {
		t.Error("expected cycle for b→a when a→b exists")
	}
}

func TestWouldCreateCycle_TransitiveCycle(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})

	// a→b→c (a depends on b, b depends on c)
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('a', 'b', 'test')")
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('b', 'c', 'test')")

	// Would c→a (c depends on a) cycle? Yes: c→a→b→c
	wouldCycle, err := d.wouldCreateCycle(ctx, "c", "a")
	if err != nil {
		t.Fatal(err)
	}
	if !wouldCycle {
		t.Error("expected cycle for c→a when a→b→c exists")
	}
}

// --- AddEdge with cycle prevention ---

func TestAddEdge_RejectsCycle(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})

	if err := d.AddEdge(ctx, "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddEdge(ctx, "b", "c"); err != nil {
		t.Fatal(err)
	}

	// c→a would create cycle
	err := d.AddEdge(ctx, "c", "a")
	if err == nil {
		t.Fatal("expected error for cyclic edge")
	}
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("expected ErrCycleDetected, got: %v", err)
	}
}

func TestAddEdge_AllowsValidEdge(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})

	if err := d.AddEdge(ctx, "a", "b"); err != nil {
		t.Fatal(err)
	}
	// c→b is fine (parallel dependency on b)
	if err := d.AddEdge(ctx, "c", "b"); err != nil {
		t.Fatalf("expected valid edge, got: %v", err)
	}
}

func TestAddEdge_RejectsSelfEdge(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	err := d.AddEdge(ctx, "a", "a")
	if err == nil {
		t.Fatal("expected error for self-edge")
	}
}

// --- DetectCycles ---

func TestDetectCycles_NoCycles(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})
	d.AddEdge(ctx, "a", "b")
	d.AddEdge(ctx, "b", "c")

	cycles, err := d.DetectCycles(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(cycles) != 0 {
		t.Fatalf("expected no cycles, got %v", cycles)
	}
}

func TestDetectCycles_FindsCycle(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})

	// Force a cycle via raw SQL (bypassing AddEdge protection)
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('a', 'b', 'test')")
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('b', 'c', 'test')")
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('c', 'a', 'test')")

	cycles, err := d.DetectCycles(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(cycles) == 0 {
		t.Fatal("expected at least one cycle")
	}
	// The cycle should contain a, b, c
	found := make(map[string]bool)
	for _, id := range cycles[0] {
		found[id] = true
	}
	if !found["a"] || !found["b"] || !found["c"] {
		t.Fatalf("cycle should contain a, b, c; got %v", cycles[0])
	}
}

func TestDetectCycles_EmptyProject(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	cycles, err := d.DetectCycles(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(cycles) != 0 {
		t.Fatalf("expected no cycles for empty project, got %v", cycles)
	}
}

// --- TopologicalSort ---

func TestTopologicalSort_LinearChain(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})
	d.AddEdge(ctx, "a", "b") // a depends on b
	d.AddEdge(ctx, "b", "c") // b depends on c

	sorted, err := d.TopologicalSort(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(sorted))
	}

	// c must come before b, b before a
	pos := make(map[string]int)
	for i, id := range sorted {
		pos[id] = i
	}
	if pos["c"] >= pos["b"] {
		t.Errorf("c should come before b: %v", sorted)
	}
	if pos["b"] >= pos["a"] {
		t.Errorf("b should come before a: %v", sorted)
	}
}

func TestTopologicalSort_Diamond(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// Diamond: d depends on b and c, both depend on a
	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})
	d.CreateTask(ctx, Task{ID: "d", Project: "p"})
	d.AddEdge(ctx, "b", "a")
	d.AddEdge(ctx, "c", "a")
	d.AddEdge(ctx, "d", "b")
	d.AddEdge(ctx, "d", "c")

	sorted, err := d.TopologicalSort(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(sorted))
	}

	pos := make(map[string]int)
	for i, id := range sorted {
		pos[id] = i
	}
	if pos["a"] >= pos["b"] || pos["a"] >= pos["c"] {
		t.Errorf("a should come before b and c: %v", sorted)
	}
	if pos["b"] >= pos["d"] || pos["c"] >= pos["d"] {
		t.Errorf("b and c should come before d: %v", sorted)
	}
}

func TestTopologicalSort_DetectsCycle(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})

	// Force cycle
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('a', 'b', 'test')")
	d.db.ExecContext(ctx, "INSERT INTO task_edges (from_task, to_task, source) VALUES ('b', 'a', 'test')")

	_, err := d.TopologicalSort(ctx, "p")
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("expected ErrCycleDetected, got: %v", err)
	}
}

func TestTopologicalSort_NoTasks(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	sorted, err := d.TopologicalSort(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 0 {
		t.Fatalf("expected empty sort, got %v", sorted)
	}
}

func TestTopologicalSort_DisconnectedNodes(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	d.CreateTask(ctx, Task{ID: "a", Project: "p"})
	d.CreateTask(ctx, Task{ID: "b", Project: "p"})
	d.CreateTask(ctx, Task{ID: "c", Project: "p"})
	// No edges — all independent

	sorted, err := d.TopologicalSort(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(sorted))
	}
}

package dag

import (
	"context"
	"fmt"
)

// ErrCycleDetected is returned when an operation would create a cycle.
var ErrCycleDetected = fmt.Errorf("cycle detected")

// wouldCreateCycle checks whether adding an edge from→to would create a cycle.
// Edge from→to means "from depends on to". A cycle exists if 'to' already
// transitively depends on 'from' through existing edges.
func (d *DAG) wouldCreateCycle(ctx context.Context, from, to string) (bool, error) {
	// Starting from 'to', follow its dependencies. If we reach 'from',
	// then adding from→to would close a loop: from→to→...→from.
	visited := make(map[string]bool)
	queue := []string{to}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == from {
			return true, nil
		}
		if visited[current] {
			continue
		}
		visited[current] = true

		deps, err := d.GetDependencies(ctx, current)
		if err != nil {
			return false, fmt.Errorf("check cycle at %s: %w", current, err)
		}
		for _, dep := range deps {
			if !visited[dep] {
				queue = append(queue, dep)
			}
		}
	}
	return false, nil
}

// DetectCycles finds all cycles in the DAG for a given project.
// Returns a list of cycles, where each cycle is a slice of task IDs forming the loop.
// Returns nil if the graph is acyclic.
func (d *DAG) DetectCycles(ctx context.Context, project string) ([][]string, error) {
	adj, err := d.buildAdjacency(ctx, project)
	if err != nil {
		return nil, err
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in current DFS path
		black = 2 // fully processed
	)

	color := make(map[string]int)
	for node := range adj {
		color[node] = white
	}

	var cycles [][]string

	var dfs func(node string, path []string)
	dfs = func(node string, path []string) {
		color[node] = gray
		path = append(path, node)

		for _, dep := range adj[node] {
			if color[dep] == gray {
				// Found cycle — extract from path
				for i, p := range path {
					if p == dep {
						cycle := make([]string, len(path)-i)
						copy(cycle, path[i:])
						cycles = append(cycles, cycle)
						break
					}
				}
			} else if color[dep] == white {
				dfs(dep, path)
			}
		}

		color[node] = black
	}

	for node := range adj {
		if color[node] == white {
			dfs(node, nil)
		}
	}

	return cycles, nil
}

// TopologicalSort returns tasks in dependency order for a project.
// Tasks with no dependencies come first. Returns ErrCycleDetected if the
// graph contains a cycle.
func (d *DAG) TopologicalSort(ctx context.Context, project string) ([]string, error) {
	adj, err := d.buildAdjacency(ctx, project)
	if err != nil {
		return nil, err
	}

	// Collect all nodes (some may only appear as dependencies)
	allNodes := make(map[string]bool)
	for node, deps := range adj {
		allNodes[node] = true
		for _, dep := range deps {
			allNodes[dep] = true
		}
	}

	// In-degree = number of prerequisites (dependencies)
	inDegree := make(map[string]int)
	for node := range allNodes {
		inDegree[node] = len(adj[node])
	}

	// Reverse adjacency: for each node, who depends on it?
	revAdj := make(map[string][]string)
	for node, deps := range adj {
		for _, dep := range deps {
			revAdj[dep] = append(revAdj[dep], node)
		}
	}

	// Kahn's algorithm: start with zero-prerequisite nodes
	var queue []string
	for node, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, node)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		sorted = append(sorted, node)

		for _, dependent := range revAdj[node] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(sorted) != len(allNodes) {
		return nil, ErrCycleDetected
	}

	return sorted, nil
}

// buildAdjacency builds a map of task_id → []dependency_ids for all tasks in a project.
func (d *DAG) buildAdjacency(ctx context.Context, project string) (map[string][]string, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT id FROM tasks WHERE project = ?", project)
	if err != nil {
		return nil, fmt.Errorf("list tasks for adjacency: %w", err)
	}
	defer rows.Close()

	adj := make(map[string][]string)
	var taskIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan task id: %w", err)
		}
		adj[id] = nil
		taskIDs = append(taskIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range taskIDs {
		deps, err := d.GetDependencies(ctx, id)
		if err != nil {
			return nil, err
		}
		adj[id] = deps
	}

	return adj, nil
}

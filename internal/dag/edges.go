package dag

import (
	"context"
	"errors"
	"fmt"
)

// Edge direction convention: from_task is the dependent, to_task is the prerequisite.
// An edge (A → B) means "A depends on B" — A cannot start until B completes.

// AddEdge creates a dependency: from depends on to. Source defaults to "beads".
func (d *DAG) AddEdge(ctx context.Context, from, to string) error {
	return d.AddEdgeWithSource(ctx, from, to, "beads")
}

// AddEdgeWithSource creates a dependency with an explicit source tag.
// Source is "beads" for hand-drawn edges or "ast" for auto-generated fences.
func (d *DAG) AddEdgeWithSource(ctx context.Context, from, to, source string) error {
	if from == to {
		return errors.New("cannot add self-edge")
	}
	_, err := d.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO task_edges (from_task, to_task, source) VALUES (?, ?, ?)",
		from, to, source)
	if err != nil {
		return fmt.Errorf("add edge: %w", err)
	}
	return nil
}

// DeleteEdgesBySource removes all edges with the given source for tasks in a project.
func (d *DAG) DeleteEdgesBySource(ctx context.Context, project, source string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM task_edges WHERE source = ? AND (
		from_task IN (SELECT id FROM tasks WHERE project = ?) OR
		to_task IN (SELECT id FROM tasks WHERE project = ?))`,
		source, project, project)
	if err != nil {
		return fmt.Errorf("delete edges by source: %w", err)
	}
	return nil
}

// GetDependencies returns task IDs that the given task depends on (to_task where from_task = id).
func (d *DAG) GetDependencies(ctx context.Context, id string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT to_task FROM task_edges WHERE from_task = ?", id)
	if err != nil {
		return nil, fmt.Errorf("get dependencies: %w", err)
	}
	defer rows.Close()
	var deps []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, fmt.Errorf("scan dependency: %w", err)
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

// GetDependents returns task IDs that depend on the given task (from_task where to_task = id).
func (d *DAG) GetDependents(ctx context.Context, id string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT from_task FROM task_edges WHERE to_task = ?", id)
	if err != nil {
		return nil, fmt.Errorf("get dependents: %w", err)
	}
	defer rows.Close()
	var deps []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, fmt.Errorf("scan dependent: %w", err)
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

// GetEdgeSource returns the source tag for an edge between two tasks.
func (d *DAG) GetEdgeSource(ctx context.Context, from, to string) (string, error) {
	var source string
	err := d.db.QueryRowContext(ctx,
		"SELECT source FROM task_edges WHERE from_task = ? AND to_task = ?",
		from, to).Scan(&source)
	if err != nil {
		return "", fmt.Errorf("get edge source %s→%s: %w", from, to, err)
	}
	return source, nil
}

// RemoveEdge removes a dependency.
func (d *DAG) RemoveEdge(ctx context.Context, from, to string) error {
	_, err := d.db.ExecContext(ctx,
		"DELETE FROM task_edges WHERE from_task = ? AND to_task = ?",
		from, to)
	if err != nil {
		return fmt.Errorf("remove edge %s→%s: %w", from, to, err)
	}
	return nil
}

// DetectCycles returns the first cycle found in the DAG, or nil if no cycle exists.
// Uses DFS with color coding: white (unvisited), gray (in current path), black (processed).
func (d *DAG) DetectCycles(ctx context.Context, project string) ([]string, error) {
	// Get all task IDs for the project
	rows, err := d.db.QueryContext(ctx, "SELECT id FROM tasks WHERE project = ?", project)
	if err != nil {
		return nil, fmt.Errorf("get tasks for project %s: %w", project, err)
	}
	defer rows.Close()

	var taskIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan task id: %w", err)
		}
		taskIDs = append(taskIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}

	// Build adjacency list
	adjList := make(map[string][]string)
	for _, id := range taskIDs {
		deps, err := d.GetDependencies(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get dependencies for %s: %w", id, err)
		}
		adjList[id] = deps
	}

	// DFS with color coding
	const (
		white = 0 // unvisited
		gray  = 1 // in current path
		black = 2 // processed
	)

	colors := make(map[string]int)
	parent := make(map[string]string)

	var dfs func(string) ([]string, bool)
	dfs = func(node string) ([]string, bool) {
		colors[node] = gray

		for _, dep := range adjList[node] {
			parent[dep] = node
			switch colors[dep] {
			case gray:
				// Back edge found - reconstruct cycle
				var cycle []string
				current := node
				for current != dep {
					cycle = append(cycle, current)
					current = parent[current]
				}
				cycle = append(cycle, dep)
				// Reverse to get proper order
				for i := len(cycle)/2 - 1; i >= 0; i-- {
					opp := len(cycle) - 1 - i
					cycle[i], cycle[opp] = cycle[opp], cycle[i]
				}
				return cycle, true
			case white:
				if cycle, found := dfs(dep); found {
					return cycle, true
				}
			}
		}

		colors[node] = black
		return nil, false
	}

	// Check all unvisited nodes
	for _, taskID := range taskIDs {
		if colors[taskID] == white {
			if cycle, found := dfs(taskID); found {
				return cycle, nil
			}
		}
	}

	return nil, nil // No cycle found
}

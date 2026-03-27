package dag

import (
	"context"
	"errors"
	"fmt"
)

// Edge direction convention: from_task is the dependent, to_task is the prerequisite.
// An edge (A → B) means "A depends on B" — A cannot start until B completes.

// AddEdge creates a dependency: from depends on to. Source defaults to "beads".
// Returns ErrCycleDetected if the edge would create a cycle.
func (d *DAG) AddEdge(ctx context.Context, from, to string) error {
	return d.AddEdgeWithSource(ctx, from, to, "beads")
}

// AddEdgeWithSource creates a dependency with an explicit source tag.
// Source is "beads" for hand-drawn edges or "ast" for auto-generated fences.
// Returns ErrCycleDetected if the edge would create a cycle.
func (d *DAG) AddEdgeWithSource(ctx context.Context, from, to, source string) error {
	if from == to {
		return errors.New("cannot add self-edge")
	}

	// Check for cycles before inserting
	wouldCycle, err := d.wouldCreateCycle(ctx, from, to)
	if err != nil {
		return fmt.Errorf("cycle check: %w", err)
	}
	if wouldCycle {
		return fmt.Errorf("%w: %s → %s", ErrCycleDetected, from, to)
	}

	_, err = d.db.ExecContext(ctx,
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

// GetProjectEdges returns all edges for tasks in a project as two maps:
// deps (from_task → []to_task) and dependents (to_task → []from_task).
// Uses a single query instead of per-task lookups.
func (d *DAG) GetProjectEdges(ctx context.Context, project string) (deps map[string][]string, dependents map[string][]string, err error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT e.from_task, e.to_task
		 FROM task_edges e
		 WHERE e.from_task IN (SELECT id FROM tasks WHERE project = ?)
		    OR e.to_task IN (SELECT id FROM tasks WHERE project = ?)`,
		project, project)
	if err != nil {
		return nil, nil, fmt.Errorf("get project edges: %w", err)
	}
	defer rows.Close()

	deps = make(map[string][]string)
	dependents = make(map[string][]string)
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, nil, fmt.Errorf("scan edge: %w", err)
		}
		deps[from] = append(deps[from], to)
		dependents[to] = append(dependents[to], from)
	}
	return deps, dependents, rows.Err()
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

package dag

import (
	"context"
	"errors"
	"fmt"
)

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
			return nil, err
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

// RemoveEdge removes a dependency.
func (d *DAG) RemoveEdge(ctx context.Context, from, to string) error {
	_, err := d.db.ExecContext(ctx,
		"DELETE FROM task_edges WHERE from_task = ? AND to_task = ?",
		from, to)
	return err
}

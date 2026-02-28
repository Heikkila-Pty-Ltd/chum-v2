package dag

import "context"

// TaskStore abstracts DAG operations used by Temporal activities.
// Enables mocking for unit tests without a real SQLite database.
type TaskStore interface {
	GetTask(ctx context.Context, id string) (Task, error)
	CreateTask(ctx context.Context, t Task) (string, error)
	UpdateTask(ctx context.Context, id string, fields map[string]any) error
	UpdateTaskStatus(ctx context.Context, id, status string) error
	CloseTask(ctx context.Context, id, status string) error
	ListTasks(ctx context.Context, project string, statuses ...string) ([]Task, error)
	GetReadyNodes(ctx context.Context, project string) ([]Task, error)
	CreateSubtasksAtomic(ctx context.Context, parentID string, tasks []Task) ([]string, error)

	// Graph edge operations (used by admission gate).
	AddEdgeWithSource(ctx context.Context, from, to, source string) error
	DeleteEdgesBySource(ctx context.Context, project, source string) error

	// Task target operations (used by admission gate).
	SetTaskTargets(ctx context.Context, taskID string, targets []TaskTarget) error
	GetTaskTargets(ctx context.Context, taskID string) ([]TaskTarget, error)
	GetAllTargetsForStatuses(ctx context.Context, project string, statuses ...string) (map[string][]TaskTarget, error)
}

// Verify *DAG satisfies TaskStore at compile time.
var _ TaskStore = (*DAG)(nil)

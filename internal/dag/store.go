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
	SetGlobalPaused(ctx context.Context, paused bool) error
	IsGlobalPaused(ctx context.Context) (bool, error)
	IsGlobalPauseSet(ctx context.Context) (paused bool, isSet bool, err error)

	// Graph edge operations.
	AddEdge(ctx context.Context, from, to string) error
	AddEdgeWithSource(ctx context.Context, from, to, source string) error
	RemoveEdge(ctx context.Context, from, to string) error
	DeleteEdgesBySource(ctx context.Context, project, source string) error
	GetDependencies(ctx context.Context, id string) ([]string, error)
	GetDependents(ctx context.Context, id string) ([]string, error)
	GetEdgeSource(ctx context.Context, from, to string) (string, error)

	// Task target operations (used by admission gate).
	SetTaskTargets(ctx context.Context, taskID string, targets []TaskTarget) error
	GetTaskTargets(ctx context.Context, taskID string) ([]TaskTarget, error)
	GetAllTargetsForStatuses(ctx context.Context, project string, statuses ...string) (map[string][]TaskTarget, error)
}

// DecisionStore abstracts decision DAG operations used by planning activities.
type DecisionStore interface {
	CreateDecision(ctx context.Context, dec Decision) (string, error)
	GetDecision(ctx context.Context, id string) (Decision, error)
	ListDecisionsForTask(ctx context.Context, taskID string) ([]Decision, error)
	CreateAlternative(ctx context.Context, alt Alternative) (string, error)
	ListAlternatives(ctx context.Context, decisionID string) ([]Alternative, error)
	GetSelectedAlternative(ctx context.Context, decisionID string) (Alternative, error)
	SelectAlternative(ctx context.Context, decisionID, alternativeID string) error
	UpdateAlternativeUCT(ctx context.Context, id string, score float64, visits int, reward float64) error
}

// PlanningStore persists reviewable planning artifacts and phase snapshots.
type PlanningStore interface {
	UpsertPlanningSnapshot(ctx context.Context, snapshot PlanningSnapshot) error
	GetPlanningSnapshot(ctx context.Context, sessionID string) (PlanningSnapshot, error)
	GetLatestPlanningSnapshotForTask(ctx context.Context, taskID string) (PlanningSnapshot, error)
	ListPlanningSnapshotsForTask(ctx context.Context, taskID string) ([]PlanningSnapshot, error)
}

// Verify *DAG satisfies TaskStore, DecisionStore, and PlanningStore at compile time.
var _ TaskStore = (*DAG)(nil)
var _ DecisionStore = (*DAG)(nil)
var _ PlanningStore = (*DAG)(nil)

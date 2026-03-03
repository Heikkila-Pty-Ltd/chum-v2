package store

import (
	"context"
	"time"
)

// TraceStore tracks execution traces and graph trace events for workflow runs.
// Execution traces are stage-spanning records of workflow task attempts.
// Graph trace events form a tree of LLM calls, tool executions, and phase boundaries.
type TraceStore interface {
	// Execution trace lifecycle.
	StartExecutionTrace(taskID, species, goalSignature string) (int64, error)
	AppendTraceEvent(traceID int64, event TraceEvent) error
	CompleteExecutionTrace(traceID int64, status, outcome string, attemptCount, successCount int) error
	ListExecutionTraces(taskID string) ([]ExecutionTrace, error)
	GetTraceEvents(traceID int64) ([]TraceEvent, error)

	// Graph trace events (tree-structured execution recording).
	RecordGraphTraceEvent(ctx context.Context, event *GraphTraceEvent) (string, error)
	GetGraphTraceEvent(ctx context.Context, eventID string) (*GraphTraceEvent, error)
	GetSessionTraceEvents(ctx context.Context, sessionID string) ([]*GraphTraceEvent, error)
	UpdateGraphTraceEvent(ctx context.Context, eventID string, updates GraphTraceEvent) error
	BackpropagateReward(ctx context.Context, sessionID string, terminalReward float64) error
	GetToolSequence(ctx context.Context, sessionID string) ([]string, error)
	GetSuccessfulSessions(ctx context.Context, minReward float64) ([]string, error)
	ExtractSolutionPath(ctx context.Context, terminalEventID string) ([]*GraphTraceEvent, error)
}

// SafetyStore covers safety blocks and morsel validation guards.
// Safety blocks are time-bounded guards that prevent actions on specific scopes
// (e.g., circuit breakers, quarantines, rate limiters).
type SafetyStore interface {
	GetBlock(scope, blockType string) (*SafetyBlock, error)
	SetBlock(scope, blockType string, blockedUntil time.Time, reason string) error
	SetBlockWithMetadata(scope, blockType string, blockedUntil time.Time, reason string, metadata map[string]interface{}) error
	RemoveBlock(scope, blockType string) error
	GetActiveBlocks() ([]SafetyBlock, error)
	GetBlockCountsByType() (map[string]int, error)

	// Morsel-level validation state.
	IsMorselValidating(morselID string) (bool, error)
	SetMorselValidating(morselID string, until time.Time) error
	ClearMorselValidating(morselID string) error
}

// LessonStore covers lesson persistence and full-text search.
// Lessons are extracted insights from workflow runs (patterns, antipatterns, rules).
type LessonStore interface {
	StoreLesson(morselID, project, category, summary, detail string, filePaths []string, labels []string) (int64, error)
	SearchLessons(query string, limit int) ([]StoredLesson, error)
	SearchLessonsByFilePath(filePaths []string, limit int) ([]StoredLesson, error)
	GetRecentLessons(project string, limit int) ([]StoredLesson, error)
	GetLessonsByMorsel(morselID string) ([]StoredLesson, error)
	CountLessons(project string) (int, error)
}

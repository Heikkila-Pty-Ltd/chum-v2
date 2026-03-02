package store

import "time"

// ExecutionTrace is a durable, stage-spanning trace of a workflow task attempt.
type ExecutionTrace struct {
	ID            int64
	TaskID        string
	Species       string
	GoalSignature string
	Status        string
	StartedAt     time.Time
	CompletedAt   time.Time
	Outcome       string
	AttemptCount  int
	SuccessCount  int
	SuccessRate   float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TraceEvent is one normalized event within an execution trace.
type TraceEvent struct {
	ID            int64
	TraceID       int64
	Stage         string
	Step          string
	Tool          string
	Command       string
	InputSummary  string
	OutputSummary string
	DurationMs    int64
	Success       bool
	ErrorContext  string
	CreatedAt     time.Time
}

// GraphTraceEvent represents a single event in the execution graph.
// Events form a tree via ParentEventID, capturing LLM calls,
// tool executions, human feedback, and phase boundaries.
type GraphTraceEvent struct {
	EventID        string   `json:"event_id"`
	ParentEventID  string   `json:"parent_event_id"`
	SessionID      string   `json:"session_id"`
	Timestamp      int64    `json:"timestamp"`
	Depth          int      `json:"depth"`
	EventType      string   `json:"event_type"` // llm_call | tool_call | human_feedback | phase_boundary
	Phase          string   `json:"phase"`      // plan | execute | review | dod | record
	ModelName      string   `json:"model_name,omitempty"`
	TokensInput    int      `json:"tokens_input,omitempty"`
	TokensOutput   int      `json:"tokens_output,omitempty"`
	ToolName       string   `json:"tool_name,omitempty"`
	ToolSuccess    *bool    `json:"tool_success,omitempty"`
	HumanMessage   string   `json:"human_message,omitempty"`
	Reward         float64  `json:"reward"`
	TerminalReward *float64 `json:"terminal_reward,omitempty"`
	IsTerminal     bool     `json:"is_terminal"`
	Metadata       string   `json:"metadata,omitempty"`
}

// SafetyBlock represents a time-bounded guard preventing actions on a scope.
type SafetyBlock struct {
	Scope        string
	BlockType    string
	BlockedUntil time.Time
	Reason       string
	Metadata     map[string]interface{}
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StoredLesson is a lesson persisted with FTS5 indexing.
type StoredLesson struct {
	ID        int64
	MorselID  string
	Project   string
	Category  string // pattern, antipattern, rule, insight
	Summary   string
	Detail    string
	FilePaths []string
	Labels    []string
	CreatedAt time.Time
}

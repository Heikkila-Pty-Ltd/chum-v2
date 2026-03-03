package store

// schemaTraces contains tables for execution tracing, graph trace events,
// and crystal candidate extraction.
const schemaTraces = `
CREATE TABLE IF NOT EXISTS execution_traces (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	profile TEXT NOT NULL DEFAULT '',
	goal_signature TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'running',
	outcome TEXT NOT NULL DEFAULT '',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	success_count INTEGER NOT NULL DEFAULT 0,
	success_rate REAL NOT NULL DEFAULT 0,
	started_at DATETIME NOT NULL DEFAULT (datetime('now')),
	completed_at DATETIME,
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS trace_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	trace_id INTEGER NOT NULL REFERENCES execution_traces(id) ON DELETE CASCADE,
	stage TEXT NOT NULL,
	step TEXT NOT NULL,
	tool TEXT NOT NULL,
	command TEXT NOT NULL,
	input_summary TEXT NOT NULL DEFAULT '',
	output_summary TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	success INTEGER NOT NULL DEFAULT 0,
	error_context TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS graph_trace_events (
	event_id TEXT PRIMARY KEY,
	parent_event_id TEXT,
	session_id TEXT NOT NULL,
	timestamp INTEGER NOT NULL,
	depth INTEGER DEFAULT 0,
	event_type TEXT NOT NULL,
	phase TEXT,
	model_name TEXT,
	tokens_input INTEGER DEFAULT 0,
	tokens_output INTEGER DEFAULT 0,
	tool_name TEXT,
	tool_success INTEGER,
	human_message TEXT,
	reward REAL DEFAULT 0.0,
	terminal_reward REAL,
	is_terminal INTEGER DEFAULT 0,
	metadata TEXT,
	FOREIGN KEY(parent_event_id) REFERENCES graph_trace_events(event_id)
);

CREATE INDEX IF NOT EXISTS idx_execution_traces_task ON execution_traces(task_id);
CREATE INDEX IF NOT EXISTS idx_execution_traces_profile ON execution_traces(profile);
CREATE INDEX IF NOT EXISTS idx_execution_traces_status ON execution_traces(status);
CREATE INDEX IF NOT EXISTS idx_trace_events_trace_id ON trace_events(trace_id);
CREATE INDEX IF NOT EXISTS idx_trace_events_stage ON trace_events(stage);
CREATE INDEX IF NOT EXISTS idx_graph_trace_session ON graph_trace_events(session_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_parent ON graph_trace_events(parent_event_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_type ON graph_trace_events(event_type);
CREATE INDEX IF NOT EXISTS idx_graph_trace_terminal ON graph_trace_events(terminal_reward DESC) WHERE is_terminal = 1;
`

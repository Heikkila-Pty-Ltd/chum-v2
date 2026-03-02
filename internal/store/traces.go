package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// StartExecutionTrace creates a new trace row for a workflow task.
func (s *Store) StartExecutionTrace(taskID, species, goalSignature string) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO execution_traces (task_id, species, goal_signature)
		VALUES (?, ?, ?)`,
		strings.TrimSpace(taskID),
		strings.TrimSpace(species),
		strings.TrimSpace(goalSignature),
	)
	if err != nil {
		return 0, fmt.Errorf("store: start execution trace: %w", err)
	}
	return result.LastInsertId()
}

// AppendTraceEvent appends a normalized event to an execution trace.
func (s *Store) AppendTraceEvent(traceID int64, event TraceEvent) error {
	successInt := 0
	if event.Success {
		successInt = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO trace_events (
			trace_id, stage, step, tool, command, input_summary, output_summary,
			duration_ms, success, error_context
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		traceID, event.Stage, event.Step, event.Tool, event.Command,
		event.InputSummary, event.OutputSummary, event.DurationMs, successInt, event.ErrorContext,
	)
	if err != nil {
		return fmt.Errorf("store: append trace event: %w", err)
	}
	return nil
}

// CompleteExecutionTrace marks a trace as complete with final metrics.
func (s *Store) CompleteExecutionTrace(traceID int64, status, outcome string, attemptCount, successCount int) error {
	successRate := 0.0
	if attemptCount > 0 {
		successRate = float64(successCount) / float64(attemptCount)
	}
	_, err := s.db.Exec(`
		UPDATE execution_traces
		SET status = ?, outcome = ?, attempt_count = ?, success_count = ?,
		    success_rate = ?, completed_at = datetime('now'), updated_at = datetime('now')
		WHERE id = ?`,
		status, outcome, attemptCount, successCount, successRate, traceID,
	)
	if err != nil {
		return fmt.Errorf("store: complete execution trace: %w", err)
	}
	return nil
}

// ListExecutionTraces returns all traces for a task, oldest first.
func (s *Store) ListExecutionTraces(taskID string) ([]ExecutionTrace, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, species, goal_signature, status, started_at, completed_at,
		       outcome, attempt_count, success_count, success_rate, created_at, updated_at
		FROM execution_traces WHERE task_id = ? ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: list execution traces: %w", err)
	}
	defer rows.Close()

	var traces []ExecutionTrace
	for rows.Next() {
		var t ExecutionTrace
		var completed sql.NullTime
		if err := rows.Scan(
			&t.ID, &t.TaskID, &t.Species, &t.GoalSignature, &t.Status,
			&t.StartedAt, &completed, &t.Outcome, &t.AttemptCount,
			&t.SuccessCount, &t.SuccessRate, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan execution trace: %w", err)
		}
		if completed.Valid {
			t.CompletedAt = completed.Time
		}
		traces = append(traces, t)
	}
	return traces, rows.Err()
}

// GetTraceEvents returns all events for a trace, oldest first.
func (s *Store) GetTraceEvents(traceID int64) ([]TraceEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, trace_id, stage, step, tool, command, input_summary,
		       output_summary, duration_ms, success, error_context, created_at
		FROM trace_events WHERE trace_id = ? ORDER BY created_at ASC`, traceID)
	if err != nil {
		return nil, fmt.Errorf("store: get trace events: %w", err)
	}
	defer rows.Close()

	var events []TraceEvent
	for rows.Next() {
		var e TraceEvent
		var success int
		if err := rows.Scan(
			&e.ID, &e.TraceID, &e.Stage, &e.Step, &e.Tool, &e.Command,
			&e.InputSummary, &e.OutputSummary, &e.DurationMs, &success,
			&e.ErrorContext, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan trace event: %w", err)
		}
		e.Success = success == 1
		events = append(events, e)
	}
	return events, rows.Err()
}

// RecordGraphTraceEvent inserts a new trace event into the execution graph.
func (s *Store) RecordGraphTraceEvent(ctx context.Context, event *GraphTraceEvent) (string, error) {
	if event.EventID == "" {
		event.EventID = generateEventID()
	}
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().Unix()
	}

	// Auto-calculate depth from parent.
	if event.ParentEventID != "" && event.Depth == 0 {
		var parentDepth int
		err := s.db.QueryRowContext(ctx,
			`SELECT depth FROM graph_trace_events WHERE event_id = ?`,
			event.ParentEventID).Scan(&parentDepth)
		if err == nil {
			event.Depth = parentDepth + 1
		}
	}

	var toolSuccessInt *int
	if event.ToolSuccess != nil {
		val := 0
		if *event.ToolSuccess {
			val = 1
		}
		toolSuccessInt = &val
	}

	isTerminalInt := 0
	if event.IsTerminal {
		isTerminalInt = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO graph_trace_events (
			event_id, parent_event_id, session_id, timestamp, depth,
			event_type, phase, model_name, tokens_input, tokens_output,
			tool_name, tool_success, human_message, reward, terminal_reward,
			is_terminal, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID, event.ParentEventID, event.SessionID, event.Timestamp, event.Depth,
		event.EventType, event.Phase, event.ModelName, event.TokensInput, event.TokensOutput,
		event.ToolName, toolSuccessInt, event.HumanMessage, event.Reward, event.TerminalReward,
		isTerminalInt, event.Metadata,
	)
	if err != nil {
		return "", fmt.Errorf("store: record graph trace event: %w", err)
	}
	return event.EventID, nil
}

// GetGraphTraceEvent retrieves a single event by ID.
func (s *Store) GetGraphTraceEvent(ctx context.Context, eventID string) (*GraphTraceEvent, error) {
	var event GraphTraceEvent
	var toolSuccessInt *int
	var terminalReward *float64
	var isTerminalInt int

	err := s.db.QueryRowContext(ctx, `
		SELECT event_id, parent_event_id, session_id, timestamp, depth,
		       event_type, phase, model_name, tokens_input, tokens_output,
		       tool_name, tool_success, human_message, reward, terminal_reward,
		       is_terminal, metadata
		FROM graph_trace_events WHERE event_id = ?`, eventID,
	).Scan(
		&event.EventID, &event.ParentEventID, &event.SessionID, &event.Timestamp, &event.Depth,
		&event.EventType, &event.Phase, &event.ModelName, &event.TokensInput, &event.TokensOutput,
		&event.ToolName, &toolSuccessInt, &event.HumanMessage, &event.Reward, &terminalReward,
		&isTerminalInt, &event.Metadata,
	)
	if err != nil {
		return nil, err
	}
	if toolSuccessInt != nil {
		val := *toolSuccessInt == 1
		event.ToolSuccess = &val
	}
	event.TerminalReward = terminalReward
	event.IsTerminal = isTerminalInt == 1
	return &event, nil
}

// GetSessionTraceEvents retrieves all events for a session, ordered by timestamp.
func (s *Store) GetSessionTraceEvents(ctx context.Context, sessionID string) ([]*GraphTraceEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, parent_event_id, session_id, timestamp, depth,
		       event_type, phase, model_name, tokens_input, tokens_output,
		       tool_name, tool_success, human_message, reward, terminal_reward,
		       is_terminal, metadata
		FROM graph_trace_events WHERE session_id = ? ORDER BY timestamp ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGraphEvents(rows)
}

// UpdateGraphTraceEvent updates fields of an existing trace event.
func (s *Store) UpdateGraphTraceEvent(ctx context.Context, eventID string, updates GraphTraceEvent) error {
	var setClauses []string
	var args []interface{}

	if updates.Reward != 0 {
		setClauses = append(setClauses, "reward = ?")
		args = append(args, updates.Reward)
	}
	if updates.TerminalReward != nil {
		setClauses = append(setClauses, "terminal_reward = ?")
		args = append(args, *updates.TerminalReward)
	}
	if updates.IsTerminal {
		setClauses = append(setClauses, "is_terminal = 1")
	}
	if updates.TokensOutput > 0 {
		setClauses = append(setClauses, "tokens_output = ?")
		args = append(args, updates.TokensOutput)
	}
	if updates.ToolSuccess != nil {
		val := 0
		if *updates.ToolSuccess {
			val = 1
		}
		setClauses = append(setClauses, "tool_success = ?")
		args = append(args, val)
	}
	if updates.Metadata != "" {
		setClauses = append(setClauses, "metadata = ?")
		args = append(args, updates.Metadata)
	}

	if len(setClauses) == 0 {
		return nil
	}

	query := "UPDATE graph_trace_events SET " + strings.Join(setClauses, ", ") + " WHERE event_id = ?"
	args = append(args, eventID)
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// BackpropagateReward sets terminal_reward on all events in a session.
func (s *Store) BackpropagateReward(ctx context.Context, sessionID string, terminalReward float64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE graph_trace_events SET terminal_reward = ? WHERE session_id = ?`,
		terminalReward, sessionID)
	return err
}

// GetToolSequence extracts the ordered tool call names for a session.
func (s *Store) GetToolSequence(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tool_name FROM graph_trace_events
		WHERE session_id = ? AND event_type = 'tool_call' AND tool_name != ''
		ORDER BY timestamp ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tools []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tools = append(tools, name)
	}
	return tools, rows.Err()
}

// GetSuccessfulSessions returns session IDs with terminal_reward >= minReward.
func (s *Store) GetSuccessfulSessions(ctx context.Context, minReward float64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT session_id FROM graph_trace_events
		WHERE is_terminal = 1 AND terminal_reward >= ?`, minReward)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		sessions = append(sessions, id)
	}
	return sessions, rows.Err()
}

// ExtractSolutionPath walks from a terminal event back to root.
func (s *Store) ExtractSolutionPath(ctx context.Context, terminalEventID string) ([]*GraphTraceEvent, error) {
	var path []*GraphTraceEvent
	currentID := terminalEventID
	for currentID != "" {
		event, err := s.GetGraphTraceEvent(ctx, currentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			return nil, err
		}
		path = append([]*GraphTraceEvent{event}, path...)
		currentID = event.ParentEventID
	}
	return path, nil
}

// RecordTraceMetadata stores arbitrary metadata on a trace event.
func (s *Store) RecordTraceMetadata(ctx context.Context, eventID string, metadata map[string]interface{}) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("store: marshal metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE graph_trace_events SET metadata = ? WHERE event_id = ?`,
		string(data), eventID)
	return err
}

// scanGraphEvents scans rows into GraphTraceEvent slices.
func scanGraphEvents(rows *sql.Rows) ([]*GraphTraceEvent, error) {
	var events []*GraphTraceEvent
	for rows.Next() {
		var event GraphTraceEvent
		var toolSuccessInt *int
		var terminalReward *float64
		var isTerminalInt int

		if err := rows.Scan(
			&event.EventID, &event.ParentEventID, &event.SessionID, &event.Timestamp, &event.Depth,
			&event.EventType, &event.Phase, &event.ModelName, &event.TokensInput, &event.TokensOutput,
			&event.ToolName, &toolSuccessInt, &event.HumanMessage, &event.Reward, &terminalReward,
			&isTerminalInt, &event.Metadata,
		); err != nil {
			return nil, err
		}
		if toolSuccessInt != nil {
			val := *toolSuccessInt == 1
			event.ToolSuccess = &val
		}
		event.TerminalReward = terminalReward
		event.IsTerminal = isTerminalInt == 1
		events = append(events, &event)
	}
	return events, rows.Err()
}

// generateEventID creates a cryptographically random event ID.
func generateEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return hex.EncodeToString(b)
}

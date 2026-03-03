package store

import (
	"testing"
)

func TestExecutionTraceLifecycle(t *testing.T) {
	s := tempStore(t)

	// Start a trace.
	traceID, err := s.StartExecutionTrace("task-1", "coder", "fix-bug")
	if err != nil {
		t.Fatalf("StartExecutionTrace: %v", err)
	}
	if traceID <= 0 {
		t.Fatalf("expected positive trace ID, got %d", traceID)
	}

	// Append events.
	err = s.AppendTraceEvent(traceID, TraceEvent{
		Stage: "plan", Step: "analyze", Tool: "Read",
		Command: "read main.go", DurationMs: 150, Success: true,
	})
	if err != nil {
		t.Fatalf("AppendTraceEvent: %v", err)
	}

	err = s.AppendTraceEvent(traceID, TraceEvent{
		Stage: "execute", Step: "edit", Tool: "Write",
		Command: "write fix.go", DurationMs: 300, Success: true,
	})
	if err != nil {
		t.Fatalf("AppendTraceEvent 2: %v", err)
	}

	err = s.AppendTraceEvent(traceID, TraceEvent{
		Stage: "review", Step: "test", Tool: "Bash",
		Command: "go test ./...", DurationMs: 5000, Success: false,
		ErrorContext: "TestFoo failed",
	})
	if err != nil {
		t.Fatalf("AppendTraceEvent 3: %v", err)
	}

	// Complete trace.
	err = s.CompleteExecutionTrace(traceID, "completed", "tests passed after fix", 3, 2)
	if err != nil {
		t.Fatalf("CompleteExecutionTrace: %v", err)
	}

	// List traces.
	traces, err := s.ListExecutionTraces("task-1")
	if err != nil {
		t.Fatalf("ListExecutionTraces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	tr := traces[0]
	if tr.Profile != "coder" {
		t.Errorf("profile = %q, want coder", tr.Profile)
	}
	if tr.Status != "completed" {
		t.Errorf("status = %q, want completed", tr.Status)
	}
	if tr.AttemptCount != 3 {
		t.Errorf("attempt_count = %d, want 3", tr.AttemptCount)
	}
	if tr.SuccessRate < 0.66 || tr.SuccessRate > 0.67 {
		t.Errorf("success_rate = %f, want ~0.667", tr.SuccessRate)
	}

	// Get events.
	events, err := s.GetTraceEvents(traceID)
	if err != nil {
		t.Fatalf("GetTraceEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Stage != "plan" || events[0].Tool != "Read" {
		t.Errorf("first event: stage=%q tool=%q", events[0].Stage, events[0].Tool)
	}
	if events[2].Success {
		t.Error("third event should have success=false")
	}
	if events[2].ErrorContext != "TestFoo failed" {
		t.Errorf("error_context = %q", events[2].ErrorContext)
	}
}

func TestListExecutionTracesEmpty(t *testing.T) {
	s := tempStore(t)
	traces, err := s.ListExecutionTraces("nonexistent")
	if err != nil {
		t.Fatalf("ListExecutionTraces: %v", err)
	}
	if len(traces) != 0 {
		t.Fatalf("expected 0 traces, got %d", len(traces))
	}
}

func TestGraphTraceEventLifecycle(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()
	sessionID := "test-session-123"

	// Record root event.
	rootID, err := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: sessionID, EventType: "phase_boundary", Phase: "plan",
	})
	if err != nil {
		t.Fatalf("record root: %v", err)
	}
	if rootID == "" {
		t.Fatal("expected generated event ID")
	}

	// Record child LLM call.
	llmID, err := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: sessionID, ParentEventID: rootID,
		EventType: "llm_call", Phase: "plan",
		ModelName: "claude-sonnet-4", TokensInput: 1000, TokensOutput: 500,
		Reward: 0.5,
	})
	if err != nil {
		t.Fatalf("record llm event: %v", err)
	}

	// Record grandchild tool call.
	toolSuccess := true
	_, err = s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: sessionID, ParentEventID: llmID,
		EventType: "tool_call", Phase: "plan",
		ToolName: "Read", ToolSuccess: &toolSuccess, Reward: 0.3,
	})
	if err != nil {
		t.Fatalf("record tool event: %v", err)
	}

	// Get session events.
	events, err := s.GetSessionTraceEvents(ctx, sessionID)
	if err != nil {
		t.Fatalf("get session events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Verify depth.
	if events[0].Depth != 0 {
		t.Errorf("root depth = %d, want 0", events[0].Depth)
	}
	if events[1].Depth != 1 {
		t.Errorf("child depth = %d, want 1", events[1].Depth)
	}
	if events[2].Depth != 2 {
		t.Errorf("grandchild depth = %d, want 2", events[2].Depth)
	}

	// Verify tool success.
	if events[2].ToolSuccess == nil || !*events[2].ToolSuccess {
		t.Error("expected tool success = true")
	}
}

func TestGraphTraceToolSequence(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()
	sessionID := "test-tools"

	tools := []string{"Read", "Grep", "Write", "Edit", "Bash"}
	for _, name := range tools {
		success := true
		_, err := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
			SessionID: sessionID, EventType: "tool_call", Phase: "execute",
			ToolName: name, ToolSuccess: &success,
		})
		if err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}

	seq, err := s.GetToolSequence(ctx, sessionID)
	if err != nil {
		t.Fatalf("get tool sequence: %v", err)
	}
	if len(seq) != len(tools) {
		t.Fatalf("expected %d tools, got %d", len(tools), len(seq))
	}
	for i, tool := range tools {
		if seq[i] != tool {
			t.Errorf("tool[%d] = %s, want %s", i, seq[i], tool)
		}
	}
}

func TestGraphTraceBackpropagateReward(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()
	sessionID := "test-backprop"

	rootID, _ := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: sessionID, EventType: "phase_boundary", Phase: "plan",
	})
	childID, _ := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: sessionID, ParentEventID: rootID, EventType: "llm_call", Phase: "plan",
	})
	s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: sessionID, ParentEventID: childID, EventType: "tool_call",
		Phase: "plan", IsTerminal: true,
	})

	if err := s.BackpropagateReward(ctx, sessionID, 1.0); err != nil {
		t.Fatalf("backpropagate: %v", err)
	}

	events, _ := s.GetSessionTraceEvents(ctx, sessionID)
	for _, e := range events {
		if e.TerminalReward == nil {
			t.Errorf("event %s missing terminal_reward", e.EventID)
		} else if *e.TerminalReward != 1.0 {
			t.Errorf("event %s terminal_reward = %f, want 1.0", e.EventID, *e.TerminalReward)
		}
	}
}

func TestGraphTraceSuccessfulSessions(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	reward09 := 0.9
	reward02 := 0.2
	s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: "good", EventType: "phase_boundary", Phase: "record",
		IsTerminal: true, TerminalReward: &reward09,
	})
	s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: "bad", EventType: "phase_boundary", Phase: "record",
		IsTerminal: true, TerminalReward: &reward02,
	})

	sessions, err := s.GetSuccessfulSessions(ctx, 0.8)
	if err != nil {
		t.Fatalf("get successful sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0] != "good" {
		t.Fatalf("expected [good], got %v", sessions)
	}
}

func TestGraphTraceExtractSolutionPath(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	rootID, _ := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: "path-test", EventType: "phase_boundary", Phase: "plan",
	})
	midID, _ := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: "path-test", ParentEventID: rootID, EventType: "llm_call", Phase: "execute",
	})
	termID, _ := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: "path-test", ParentEventID: midID, EventType: "phase_boundary",
		Phase: "dod", IsTerminal: true,
	})

	path, err := s.ExtractSolutionPath(ctx, termID)
	if err != nil {
		t.Fatalf("extract path: %v", err)
	}
	if len(path) != 3 {
		t.Fatalf("expected path length 3, got %d", len(path))
	}
	if path[0].EventID != rootID {
		t.Error("first event should be root")
	}
	if path[2].EventID != termID {
		t.Error("last event should be terminal")
	}
}

func TestGraphTraceMetadata(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	eventID, _ := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: "meta-test", EventType: "llm_call", Phase: "plan",
	})

	err := s.RecordTraceMetadata(ctx, eventID, map[string]interface{}{
		"thinking_level": "high",
		"cost_usd":       0.05,
	})
	if err != nil {
		t.Fatalf("record metadata: %v", err)
	}

	retrieved, err := s.GetGraphTraceEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if retrieved.Metadata == "" {
		t.Error("expected metadata to be set")
	}
}

func TestGraphTraceUpdateEvent(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	eventID, _ := s.RecordGraphTraceEvent(ctx, &GraphTraceEvent{
		SessionID: "update-test", EventType: "llm_call", Phase: "plan",
		Reward: 0.0,
	})

	reward := 0.95
	err := s.UpdateGraphTraceEvent(ctx, eventID, GraphTraceEvent{
		Reward:         0.8,
		TerminalReward: &reward,
		IsTerminal:     true,
	})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}

	retrieved, _ := s.GetGraphTraceEvent(ctx, eventID)
	if retrieved.Reward != 0.8 {
		t.Errorf("reward = %f, want 0.8", retrieved.Reward)
	}
	if !retrieved.IsTerminal {
		t.Error("expected is_terminal = true")
	}
	if retrieved.TerminalReward == nil || *retrieved.TerminalReward != 0.95 {
		t.Error("terminal_reward not set correctly")
	}
}

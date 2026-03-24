package plansession

import (
	"log/slog"
	"os"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewManager(t *testing.T) {
	m := NewManager(testLogger(), "9999", "")
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.Token() == "" {
		t.Fatal("expected non-empty token")
	}
	if m.maxSessions != 3 {
		t.Errorf("expected maxSessions=3, got %d", m.maxSessions)
	}
}

func TestGetNotFound(t *testing.T) {
	m := NewManager(testLogger(), "9999", "")
	_, err := m.Get("nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestDestroyNotFound(t *testing.T) {
	m := NewManager(testLogger(), "9999", "")
	err := m.Destroy("nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestMaxSessionsEnforced(t *testing.T) {
	m := NewManager(testLogger(), "9999", "")
	m.maxSessions = 2

	// Manually insert sessions to simulate active ones (without tmux).
	m.sessions["plan-a"] = &Session{ID: "plan-a", PlanID: "plan-a", State: StateReady}
	m.sessions["plan-b"] = &Session{ID: "plan-b", PlanID: "plan-b", State: StateReady}

	// Third session should be rejected.
	_, err := m.Spawn("plan-c", "/tmp")
	if err != ErrMaxSessions {
		t.Errorf("expected ErrMaxSessions, got %v", err)
	}
}

func TestSpawnReturnExisting(t *testing.T) {
	m := NewManager(testLogger(), "9999", "")

	existing := &Session{ID: "plan-test", PlanID: "test", State: StateReady}
	m.sessions["test"] = existing

	got, err := m.Spawn("test", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != existing {
		t.Error("expected same session object back")
	}
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateStarting, "starting"},
		{StateReady, "ready"},
		{StateDone, "done"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a\nb\nc\n", 3},
		{"single", 1},
		{"", 0},
		{"a\n\nb\n", 2}, // empty lines skipped
	}
	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestReconcileNoTmux(t *testing.T) {
	// Reconcile should not panic when tmux server is not running.
	m := NewManager(testLogger(), "9999", "")
	m.Reconcile() // should complete without error
}

func TestBridgeSessionAccessor(t *testing.T) {
	s := &Session{}
	if s.Bridge() != nil {
		t.Error("expected nil bridge on new session")
	}
}

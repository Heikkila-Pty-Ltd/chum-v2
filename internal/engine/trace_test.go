package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/perf"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/store"
	"go.temporal.io/sdk/testsuite"
)

func tempTraceStore(t *testing.T) (*store.Store, *perf.Tracker) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test-traces.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if err := perf.Migrate(s.DB()); err != nil {
		t.Fatalf("migrate perf: %v", err)
	}
	tracker := perf.New(s.DB(), 0)
	return s, tracker
}

func TestRecordTraceActivity_Success(t *testing.T) {
	t.Parallel()
	ts, tracker := tempTraceStore(t)

	a := &Activities{Traces: ts, Perf: tracker}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.RecordTraceActivity)

	_, err := env.ExecuteActivity(a.RecordTraceActivity, TraceOutcome{
		TaskID:    "task-1",
		SessionID: "session-1",
		Agent:     "claude",
		Model:     "sonnet-4",
		Tier:      "fast",
		Reason:    string(CloseCompleted),
		SubReason: "completed",
		Duration:  90 * time.Second,
	})
	if err != nil {
		t.Fatalf("RecordTraceActivity failed: %v", err)
	}

	// Verify execution trace was recorded.
	traces, err := ts.ListExecutionTraces("task-1")
	if err != nil {
		t.Fatalf("list traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0].Status != string(CloseCompleted) {
		t.Errorf("expected status %q, got %q", CloseCompleted, traces[0].Status)
	}
	if traces[0].SuccessRate != 1.0 {
		t.Errorf("expected success_rate 1.0, got %f", traces[0].SuccessRate)
	}

	// Verify trace events were recorded.
	events, err := ts.GetTraceEvents(traces[0].ID)
	if err != nil {
		t.Fatalf("get trace events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Tool != "claude" {
		t.Errorf("expected tool %q, got %q", "claude", events[0].Tool)
	}

	// Verify perf run was recorded.
	stats, err := tracker.StatsForTier(t.Context(), "fast")
	if err != nil {
		t.Fatalf("stats for tier: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 perf stat, got %d", len(stats))
	}
	if stats[0].Successes != 1 {
		t.Errorf("expected 1 success, got %d", stats[0].Successes)
	}
}

func TestRecordTraceActivity_Failure(t *testing.T) {
	t.Parallel()
	ts, tracker := tempTraceStore(t)

	a := &Activities{Traces: ts, Perf: tracker}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.RecordTraceActivity)

	_, err := env.ExecuteActivity(a.RecordTraceActivity, TraceOutcome{
		TaskID:    "task-2",
		SessionID: "session-2",
		Agent:     "codex",
		Model:     "",
		Tier:      "balanced",
		Reason:    string(CloseDoDFailed),
		SubReason: "dod_failed",
		Duration:  30 * time.Second,
	})
	if err != nil {
		t.Fatalf("RecordTraceActivity failed: %v", err)
	}

	// Verify execution trace shows failure.
	traces, err := ts.ListExecutionTraces("task-2")
	if err != nil {
		t.Fatalf("list traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0].SuccessRate != 0.0 {
		t.Errorf("expected success_rate 0.0, got %f", traces[0].SuccessRate)
	}

	// Verify perf run shows failure.
	stats, err := tracker.StatsForTier(t.Context(), "balanced")
	if err != nil {
		t.Fatalf("stats for tier: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 perf stat, got %d", len(stats))
	}
	if stats[0].Successes != 0 {
		t.Errorf("expected 0 successes, got %d", stats[0].Successes)
	}
}

func TestRecordTraceActivity_NilStores(t *testing.T) {
	t.Parallel()

	// Should not panic with nil Traces and Perf.
	a := &Activities{Traces: nil, Perf: nil}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.RecordTraceActivity)

	_, err := env.ExecuteActivity(a.RecordTraceActivity, TraceOutcome{
		TaskID:    "task-3",
		SessionID: "session-3",
		Agent:     "claude",
		Reason:    string(CloseCompleted),
		SubReason: "completed",
		Duration:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("RecordTraceActivity with nil stores should not fail: %v", err)
	}
}

func TestRecordTraceActivity_InfraFailureSkipsPerf(t *testing.T) {
	t.Parallel()
	ts, tracker := tempTraceStore(t)

	a := &Activities{Traces: ts, Perf: tracker}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.RecordTraceActivity)

	// reviewer_error is infra noise — should record trace but NOT perf.
	_, err := env.ExecuteActivity(a.RecordTraceActivity, TraceOutcome{
		TaskID:    "task-infra",
		SessionID: "session-infra",
		Agent:     "claude",
		Model:     "sonnet-4",
		Tier:      "fast",
		Reason:    string(CloseNeedsReview),
		SubReason: "reviewer_error",
		Duration:  60 * time.Second,
	})
	if err != nil {
		t.Fatalf("RecordTraceActivity failed: %v", err)
	}

	// Trace should be recorded.
	traces, err := ts.ListExecutionTraces("task-infra")
	if err != nil {
		t.Fatalf("list traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}

	// Perf should NOT be recorded — reviewer_error is not the provider's fault.
	stats, err := tracker.StatsForTier(t.Context(), "fast")
	if err != nil {
		t.Fatalf("stats for tier: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected 0 perf stats for infra failure, got %d", len(stats))
	}
}

func TestRecordTraceActivity_DecomposedIsPerfSuccess(t *testing.T) {
	t.Parallel()
	ts, tracker := tempTraceStore(t)

	a := &Activities{Traces: ts, Perf: tracker}
	suite := testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.RecordTraceActivity)

	_, err := env.ExecuteActivity(a.RecordTraceActivity, TraceOutcome{
		TaskID:    "task-decomp",
		SessionID: "session-decomp",
		Agent:     "claude",
		Model:     "sonnet-4",
		Tier:      "balanced",
		Reason:    string(CloseDecomposed),
		SubReason: "decomposed",
		Duration:  20 * time.Second,
	})
	if err != nil {
		t.Fatalf("RecordTraceActivity failed: %v", err)
	}

	// Decomposed should be recorded as perf SUCCESS (provider correctly split work).
	stats, err := tracker.StatsForTier(t.Context(), "balanced")
	if err != nil {
		t.Fatalf("stats for tier: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 perf stat, got %d", len(stats))
	}
	if stats[0].Successes != 1 {
		t.Errorf("expected decomposed to count as perf success, got %d successes", stats[0].Successes)
	}
}

func TestIsPerfRelevant(t *testing.T) {
	t.Parallel()

	relevant := []string{"completed", "exec_failed", "dod_failed", "dod_error", "decomposed", "decompose_failed"}
	for _, r := range relevant {
		if !isPerfRelevant(r) {
			t.Errorf("isPerfRelevant(%q) = false, want true", r)
		}
	}

	irrelevant := []string{"worktree_failed", "push_failed", "pr_create_failed", "reviewer_error",
		"reviewer_modified_code", "review_submit_failed", "merge_failed", "merge_blocked",
		"no_reviewer_activity", "max_rounds_reached", "subtask_creation_failed"}
	for _, r := range irrelevant {
		if isPerfRelevant(r) {
			t.Errorf("isPerfRelevant(%q) = true, want false", r)
		}
	}
}

func TestRewardForReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason CloseReason
		want   float64
	}{
		{CloseCompleted, 1.0},
		{CloseDecomposed, 0.5},
		{CloseDoDFailed, -1.0},
		{CloseNeedsReview, -1.0},
	}
	for _, tt := range tests {
		got := rewardForReason(tt.reason)
		if got != tt.want {
			t.Errorf("rewardForReason(%q) = %f, want %f", tt.reason, got, tt.want)
		}
	}
}

package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestThemed_AllEvents(t *testing.T) {
	t.Parallel()

	events := []struct {
		event string
		extra map[string]string
		want  string // substring that must appear
	}{
		{"dispatch", map[string]string{"count": "3", "tasks": "`a`, `b`, `c`"}, "dispatched 3 tasks"},
		{"execute", map[string]string{"agent": "claude"}, "agent started"},
		{"dod_pass", nil, "DoD passed"},
		{"dod_fail", map[string]string{"failures": "go test failed"}, "DoD failed"},
		{"complete", map[string]string{"pr": "42", "review_url": "https://example.com"}, "task complete"},
		{"review", map[string]string{"reviewer": "codex", "round": "1"}, "review started"},
		{"review_approved", map[string]string{"reviewer": "codex"}, "review approved"},
		{"review_changes", map[string]string{"reviewer": "codex", "round": "2"}, "changes requested"},
		{"pr_created", map[string]string{"pr": "99", "url": "https://gh.com/pr/99"}, "PR opened"},
		{"merged", map[string]string{"pr": "99"}, "merged to main"},
		{"escalate", map[string]string{"reason": "needs_review", "sub_reason": "exec_failed"}, "task blocked"},
		{"decomposed", map[string]string{"subtasks": "3"}, "task decomposed"},
	}

	for _, tt := range events {
		t.Run(tt.event, func(t *testing.T) {
			msg := themed(tt.event, "task-1", tt.extra)
			require.Contains(t, msg, tt.want, "event=%s", tt.event)
			require.NotEmpty(t, msg)
		})
	}
}

func TestThemed_UnknownEvent(t *testing.T) {
	t.Parallel()
	msg := themed("nonexistent", "task-1", nil)
	require.Empty(t, msg)
}

func TestThemed_NilExtra(t *testing.T) {
	t.Parallel()
	msg := themed("execute", "task-1", nil)
	require.Contains(t, msg, "agent started")
}

func TestThemed_TaskIDIncluded(t *testing.T) {
	t.Parallel()
	msg := themed("execute", "chum-zdt-1", map[string]string{"agent": "codex"})
	require.Contains(t, msg, "chum-zdt-1")
}

func TestThemed_DoDFailTruncatesLongFailures(t *testing.T) {
	t.Parallel()
	longFailure := ""
	for i := 0; i < 300; i++ {
		longFailure += "x"
	}
	msg := themed("dod_fail", "task-1", map[string]string{"failures": longFailure})
	require.Contains(t, msg, "…")
	require.Less(t, len(msg), 300)
}

func TestJoinTasks(t *testing.T) {
	t.Parallel()
	require.Equal(t, "`a`, `b`", joinTasks([]string{"a", "b"}))
	require.Equal(t, "`solo`", joinTasks([]string{"solo"}))
	require.Equal(t, "", joinTasks(nil))
}

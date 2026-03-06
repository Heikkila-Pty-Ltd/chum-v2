package beadsbridge

import (
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/beads"
)

func TestEvaluateGate(t *testing.T) {
	t.Parallel()
	issue := beads.Issue{
		ID:     "bd-1",
		Title:  "Task",
		Status: "ready",
		Labels: []string{"chum-canary"},
	}
	got := EvaluateGate(issue, "chum-canary")
	if !got.Pass || got.Reason != "gate_pass" {
		t.Fatalf("unexpected gate pass result: %+v", got)
	}

	issue.Labels = nil
	got = EvaluateGate(issue, "chum-canary")
	if got.Pass || got.Reason != "no_canary_label" {
		t.Fatalf("unexpected gate failure result: %+v", got)
	}
}

func TestEvaluateGate_CHUMGeneratedBypassesCanaryLabel(t *testing.T) {
	t.Parallel()
	issue := beads.Issue{
		ID:        "bd-2",
		Title:     "System-generated task",
		Status:    "open",
		Owner:     "chum@localhost",
		CreatedBy: "CHUM v2",
	}
	got := EvaluateGate(issue, "chum-canary")
	if !got.Pass || got.Reason != "gate_pass" {
		t.Fatalf("expected CHUM-generated issue to bypass canary gate, got %+v", got)
	}
}

func TestFingerprintIssue_Deterministic(t *testing.T) {
	t.Parallel()
	a := beads.Issue{
		ID:                 "bd-1",
		Title:              "Task",
		Description:        "desc",
		Status:             "ready",
		Priority:           1,
		IssueType:          "task",
		Labels:             []string{"b", "a"},
		Dependencies:       []beads.Dependency{{DependsOnID: "bd-2"}, {DependsOnID: "bd-3"}},
		AcceptanceCriteria: "done",
	}
	b := a
	b.Labels = []string{"a", "b"}
	b.Dependencies = []beads.Dependency{{DependsOnID: "bd-3"}, {DependsOnID: "bd-2"}}
	if FingerprintIssue(a) != FingerprintIssue(b) {
		t.Fatalf("fingerprint should be stable regardless of label/dependency ordering")
	}
}

package planning

import "testing"

func TestPhaseConstants(t *testing.T) {
	t.Parallel()
	phases := []Phase{
		PhaseGoalClarification,
		PhaseResearch,
		PhaseGoalCheck,
		PhasePushApproaches,
		PhaseInteractive,
		PhaseDecompose,
		PhaseApproveDecomp,
		PhaseHandoff,
		PhaseCancelled,
		PhaseCompleted,
	}
	seen := make(map[Phase]bool)
	for _, p := range phases {
		if p == "" {
			t.Fatal("empty phase constant")
		}
		if seen[p] {
			t.Fatalf("duplicate phase constant: %s", p)
		}
		seen[p] = true
	}
}

func TestSignalNameConstants(t *testing.T) {
	t.Parallel()
	names := []string{
		SignalNameSelect,
		SignalNameDig,
		SignalNameQuestion,
		SignalNameGreenlight,
		SignalNameApproveDecomp,
		SignalNameCancel,
	}
	seen := make(map[string]bool)
	for _, n := range names {
		if n == "" {
			t.Fatal("empty signal name")
		}
		if seen[n] {
			t.Fatalf("duplicate signal name: %s", n)
		}
		seen[n] = true
	}
}

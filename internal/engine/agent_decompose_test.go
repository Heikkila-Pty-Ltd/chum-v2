package engine

import (
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
	"go.temporal.io/sdk/workflow"
)

// dummyActivityOpts returns zero-value activity options for signature compatibility.
// Actual recursive re-decomposition is tested at the workflow integration level.
func dummyActivityOpts() workflow.ActivityOptions {
	return workflow.ActivityOptions{}
}

func TestFlattenDecomposedSteps_AllUnderThreshold(t *testing.T) {
	// Steps within the 15-min threshold should pass through unchanged.
	steps := []types.DecompStep{
		{Title: "Small task", Description: "Fix bug", Estimate: 10},
		{Title: "Another small", Description: "Add field", Estimate: 15},
	}
	// Can't call with nil context + ExecuteActivity, but we can test the
	// zero-depth case which skips the activity call.
	got, err := flattenDecomposedSteps(nil, nil, TaskRequest{}, steps, 0, dummyActivityOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 steps, got %d", len(got))
	}
}

func TestFlattenDecomposedSteps_ZeroDepthPassesThrough(t *testing.T) {
	// At depth 0, even oversized steps pass through (no re-decomposition).
	steps := []types.DecompStep{
		{Title: "Big task", Description: "Huge thing", Estimate: 60},
	}
	got, err := flattenDecomposedSteps(nil, nil, TaskRequest{}, steps, 0, dummyActivityOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want 1 step at depth 0, got %d", len(got))
	}
}

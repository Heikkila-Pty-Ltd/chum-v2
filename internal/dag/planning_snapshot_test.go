package dag

import (
	"context"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

func TestPlanningSnapshot_RoundTripAndLatest(t *testing.T) {
	t.Parallel()

	d := newTestDAG(t)
	ctx := context.Background()
	if _, err := d.CreateTask(ctx, Task{ID: "goal-1", Title: "Goal", Project: "proj"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	first := PlanningSnapshot{
		SessionID: "plan-1",
		GoalID:    "goal-1",
		Project:   "proj",
		Phase:     "research",
		Status:    "awaiting_selection",
		Goal:      types.PlanningGoal{Intent: "Improve planning visibility"},
		History:   []types.PlanningPhaseEntry{{Phase: "research", Status: "awaiting_selection", Note: "first"}},
	}
	second := PlanningSnapshot{
		SessionID: "plan-2",
		GoalID:    "goal-1",
		Project:   "proj",
		Phase:     "completed",
		Status:    "completed",
		PlanSpec: &types.PlanSpec{
			ProblemStatement:   "Planning is opaque",
			DesiredOutcome:     "Reviewable plan artifacts",
			ExpectedPROutcome:  "Adds snapshot storage and API exposure",
			Summary:            "Persist snapshots before execution.",
			ChosenApproach:     types.PlanningApproach{Title: "PlanSpec"},
			NonGoals:           []string{"Workflow redesign"},
			Risks:              []string{"Overfitting the schema"},
			ValidationStrategy: []string{"Go tests"},
			Steps:              []types.DecompStep{{Title: "Persist snapshot", Description: "Store planning", Acceptance: "Readable via API", Estimate: 10}},
		},
		History: []types.PlanningPhaseEntry{{Phase: "completed", Status: "completed", Note: "done"}},
	}

	if err := d.UpsertPlanningSnapshot(ctx, first); err != nil {
		t.Fatalf("UpsertPlanningSnapshot first: %v", err)
	}
	if err := d.UpsertPlanningSnapshot(ctx, second); err != nil {
		t.Fatalf("UpsertPlanningSnapshot second: %v", err)
	}

	got, err := d.GetPlanningSnapshot(ctx, "plan-2")
	if err != nil {
		t.Fatalf("GetPlanningSnapshot: %v", err)
	}
	if got.PlanSpec == nil || got.PlanSpec.ExpectedPROutcome == "" {
		t.Fatalf("expected plan spec to round-trip, got %+v", got.PlanSpec)
	}

	latest, err := d.GetLatestPlanningSnapshotForTask(ctx, "goal-1")
	if err != nil {
		t.Fatalf("GetLatestPlanningSnapshotForTask: %v", err)
	}
	if latest.SessionID != "plan-2" {
		t.Fatalf("latest session = %q, want plan-2", latest.SessionID)
	}

	sessions, err := d.ListPlanningSnapshotsForTask(ctx, "goal-1")
	if err != nil {
		t.Fatalf("ListPlanningSnapshotsForTask: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}

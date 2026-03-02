package dag

import (
	"context"
	"database/sql"
	"testing"
)

func TestCreateDecision_GeneratesID(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "dt-1", Project: "p"})
	id, err := d.CreateDecision(ctx, Decision{
		TaskID: "dt-1",
		Title:  "which approach?",
	})
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if id == "" {
		t.Fatal("expected generated ID")
	}
}

func TestCreateDecision_UsesExplicitID(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "dt-2", Project: "p"})
	id, err := d.CreateDecision(ctx, Decision{
		ID:     "dec-explicit",
		TaskID: "dt-2",
		Title:  "explicit",
	})
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if id != "dec-explicit" {
		t.Fatalf("ID = %q, want dec-explicit", id)
	}
}

func TestGetDecision_RoundTrips(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "dt-3", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{
		ID:      "dec-rt",
		TaskID:  "dt-3",
		Title:   "decomposition strategy",
		Context: "task too large",
		Outcome: "split into 3",
	})

	got, err := d.GetDecision(ctx, "dec-rt")
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Title != "decomposition strategy" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Context != "task too large" {
		t.Fatalf("Context = %q", got.Context)
	}
	if got.Outcome != "split into 3" {
		t.Fatalf("Outcome = %q", got.Outcome)
	}
	if got.TaskID != "dt-3" {
		t.Fatalf("TaskID = %q", got.TaskID)
	}
}

func TestGetDecision_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, err := d.GetDecision(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent decision")
	}
}

func TestListDecisionsForTask(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "dt-4", Project: "p"})
	_, _ = d.CreateTask(ctx, Task{ID: "dt-5", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-a", TaskID: "dt-4", Title: "first"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-b", TaskID: "dt-4", Title: "second"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-c", TaskID: "dt-5", Title: "other task"})

	decs, err := d.ListDecisionsForTask(ctx, "dt-4")
	if err != nil {
		t.Fatalf("ListDecisionsForTask: %v", err)
	}
	if len(decs) != 2 {
		t.Fatalf("len = %d, want 2", len(decs))
	}
}

func TestListDecisionsForTask_Empty(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	decs, err := d.ListDecisionsForTask(ctx, "no-such-task")
	if err != nil {
		t.Fatalf("ListDecisionsForTask: %v", err)
	}
	if len(decs) != 0 {
		t.Fatalf("expected empty, got %d", len(decs))
	}
}

func TestCreateAlternative_RoundTrips(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "at-1", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-alt", TaskID: "at-1"})

	id, err := d.CreateAlternative(ctx, Alternative{
		DecisionID: "dec-alt",
		Label:      "approach A",
		Reasoning:  "simpler, fewer dependencies",
		Selected:   false,
		UCTScore:   1.5,
		Visits:     3,
		Reward:     4.0,
	})
	if err != nil {
		t.Fatalf("CreateAlternative: %v", err)
	}
	if id == "" {
		t.Fatal("expected generated ID")
	}

	alts, err := d.ListAlternatives(ctx, "dec-alt")
	if err != nil {
		t.Fatalf("ListAlternatives: %v", err)
	}
	if len(alts) != 1 {
		t.Fatalf("len = %d, want 1", len(alts))
	}
	if alts[0].Label != "approach A" {
		t.Fatalf("Label = %q", alts[0].Label)
	}
	if alts[0].Reasoning != "simpler, fewer dependencies" {
		t.Fatalf("Reasoning = %q", alts[0].Reasoning)
	}
	if alts[0].UCTScore != 1.5 {
		t.Fatalf("UCTScore = %f", alts[0].UCTScore)
	}
	if alts[0].Visits != 3 {
		t.Fatalf("Visits = %d", alts[0].Visits)
	}
	if alts[0].Reward != 4.0 {
		t.Fatalf("Reward = %f", alts[0].Reward)
	}
}

func TestListAlternatives_OrderedByScore(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "ord-1", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-ord", TaskID: "ord-1"})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-low", DecisionID: "dec-ord", Label: "low", UCTScore: 0.5})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-high", DecisionID: "dec-ord", Label: "high", UCTScore: 2.0})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-mid", DecisionID: "dec-ord", Label: "mid", UCTScore: 1.0})

	alts, err := d.ListAlternatives(ctx, "dec-ord")
	if err != nil {
		t.Fatalf("ListAlternatives: %v", err)
	}
	if len(alts) != 3 {
		t.Fatalf("len = %d, want 3", len(alts))
	}
	if alts[0].Label != "high" {
		t.Fatalf("first = %q, want high", alts[0].Label)
	}
	if alts[1].Label != "mid" {
		t.Fatalf("second = %q, want mid", alts[1].Label)
	}
	if alts[2].Label != "low" {
		t.Fatalf("third = %q, want low", alts[2].Label)
	}
}

func TestSelectAlternative(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "sel-1", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-sel", TaskID: "sel-1"})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-a", DecisionID: "dec-sel", Label: "option A"})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-b", DecisionID: "dec-sel", Label: "option B"})

	// Select A
	if err := d.SelectAlternative(ctx, "dec-sel", "alt-a"); err != nil {
		t.Fatalf("SelectAlternative: %v", err)
	}

	selected, err := d.GetSelectedAlternative(ctx, "dec-sel")
	if err != nil {
		t.Fatalf("GetSelectedAlternative: %v", err)
	}
	if selected.ID != "alt-a" {
		t.Fatalf("selected = %q, want alt-a", selected.ID)
	}

	// Decision outcome should be updated
	dec, _ := d.GetDecision(ctx, "dec-sel")
	if dec.Outcome != "option A" {
		t.Fatalf("Outcome = %q, want 'option A'", dec.Outcome)
	}

	// Switch to B
	if err := d.SelectAlternative(ctx, "dec-sel", "alt-b"); err != nil {
		t.Fatalf("SelectAlternative switch: %v", err)
	}

	selected, _ = d.GetSelectedAlternative(ctx, "dec-sel")
	if selected.ID != "alt-b" {
		t.Fatalf("selected = %q after switch, want alt-b", selected.ID)
	}

	// A should be deselected
	alts, _ := d.ListAlternatives(ctx, "dec-sel")
	for _, alt := range alts {
		if alt.ID == "alt-a" && alt.Selected {
			t.Fatal("alt-a should be deselected after switch")
		}
	}
}

func TestSelectAlternative_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "nf-1", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-nf", TaskID: "nf-1"})

	err := d.SelectAlternative(ctx, "dec-nf", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent alternative")
	}
}

func TestGetSelectedAlternative_NoneSelected(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "ns-1", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-ns", TaskID: "ns-1"})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-ns", DecisionID: "dec-ns"})

	_, err := d.GetSelectedAlternative(ctx, "dec-ns")
	if err == nil {
		t.Fatal("expected error when no alternative selected")
	}
}

func TestUpdateAlternativeUCT(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "uct-1", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-uct", TaskID: "uct-1"})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-uct", DecisionID: "dec-uct"})

	if err := d.UpdateAlternativeUCT(ctx, "alt-uct", 2.5, 10, 7.0); err != nil {
		t.Fatalf("UpdateAlternativeUCT: %v", err)
	}

	alts, _ := d.ListAlternatives(ctx, "dec-uct")
	if len(alts) != 1 {
		t.Fatalf("len = %d", len(alts))
	}
	if alts[0].UCTScore != 2.5 {
		t.Fatalf("UCTScore = %f, want 2.5", alts[0].UCTScore)
	}
	if alts[0].Visits != 10 {
		t.Fatalf("Visits = %d, want 10", alts[0].Visits)
	}
	if alts[0].Reward != 7.0 {
		t.Fatalf("Reward = %f, want 7.0", alts[0].Reward)
	}
}

func TestUpdateAlternativeUCT_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	err := d.UpdateAlternativeUCT(ctx, "missing", 1.0, 1, 1.0)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestDecisionCascadeDeletesOnTaskDelete(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, _ = d.CreateTask(ctx, Task{ID: "cas-1", Project: "p"})
	_, _ = d.CreateDecision(ctx, Decision{ID: "dec-cas", TaskID: "cas-1"})
	_, _ = d.CreateAlternative(ctx, Alternative{ID: "alt-cas", DecisionID: "dec-cas"})

	// Delete the task — decisions and alternatives should cascade.
	_, err := d.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", "cas-1")
	if err != nil {
		t.Fatalf("delete task: %v", err)
	}

	_, err = d.GetDecision(ctx, "dec-cas")
	if err == nil {
		t.Fatal("expected decision to be cascade-deleted")
	}

	alts, err := d.ListAlternatives(ctx, "dec-cas")
	if err != nil {
		t.Fatalf("ListAlternatives after cascade: %v", err)
	}
	if len(alts) != 0 {
		t.Fatalf("expected 0 alternatives after cascade, got %d", len(alts))
	}
}

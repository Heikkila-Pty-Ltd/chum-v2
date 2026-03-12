package dag

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCreatePlan(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	p := &PlanDoc{
		Project: "test-project",
		Title:   "Test Plan",
	}
	if err := d.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected generated ID")
	}
}

func TestCreatePlanWithBrief(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	conv := []ConversationMessage{{Role: "user", Message: "Build auth", Timestamp: "2026-01-01T00:00:00Z"}}
	convJSON, _ := json.Marshal(conv)

	p := &PlanDoc{
		Project:      "proj",
		Title:        "Auth Plan",
		Conversation: convJSON,
	}
	if err := d.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := d.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}

	var msgs []ConversationMessage
	if err := json.Unmarshal(got.Conversation, &msgs); err != nil {
		t.Fatalf("unmarshal conversation: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Message != "Build auth" {
		t.Fatalf("unexpected conversation: %v", msgs)
	}
}

func TestGetPlan(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	p := &PlanDoc{Project: "proj", Title: "My Plan"}
	if err := d.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	got, err := d.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Title != "My Plan" {
		t.Fatalf("title = %q, want %q", got.Title, "My Plan")
	}
	if got.Status != "draft" {
		t.Fatalf("status = %q, want %q", got.Status, "draft")
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("expected non-zero CreatedAt")
	}
}

func TestGetPlan_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	_, err := d.GetPlan(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plan")
	}
}

func TestListPlans(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	// Create plans in two projects.
	for _, title := range []string{"Plan A", "Plan B"} {
		p := &PlanDoc{Project: "proj1", Title: title}
		if err := d.CreatePlan(ctx, p); err != nil {
			t.Fatalf("CreatePlan(%s): %v", title, err)
		}
	}
	p := &PlanDoc{Project: "proj2", Title: "Plan C"}
	if err := d.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	plans, err := d.ListPlans(ctx, "proj1")
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(plans))
	}
}

func TestListPlans_Empty(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)

	plans, err := d.ListPlans(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("got %d plans, want 0", len(plans))
	}
}

func TestUpdatePlan(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	p := &PlanDoc{Project: "proj", Title: "Original"}
	if err := d.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	p.Title = "Updated"
	p.Status = "grooming"
	if err := d.UpdatePlan(ctx, p); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	got, err := d.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got.Title != "Updated" {
		t.Fatalf("title = %q, want %q", got.Title, "Updated")
	}
	if got.Status != "grooming" {
		t.Fatalf("status = %q, want %q", got.Status, "grooming")
	}
}

func TestAppendConversation(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)
	ctx := context.Background()

	p := &PlanDoc{Project: "proj", Title: "Chat Plan"}
	if err := d.CreatePlan(ctx, p); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	msg1 := ConversationMessage{Role: "user", Message: "Hello", Timestamp: "2026-01-01T00:00:00Z"}
	if err := d.AppendConversation(ctx, p.ID, msg1); err != nil {
		t.Fatalf("AppendConversation(1): %v", err)
	}

	msg2 := ConversationMessage{Role: "assistant", Message: "Hi there", Timestamp: "2026-01-01T00:00:01Z"}
	if err := d.AppendConversation(ctx, p.ID, msg2); err != nil {
		t.Fatalf("AppendConversation(2): %v", err)
	}

	got, err := d.GetPlan(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}

	var msgs []ConversationMessage
	if err := json.Unmarshal(got.Conversation, &msgs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("unexpected roles: %v", msgs)
	}
}

func TestAppendConversation_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestDAG(t)

	err := d.AppendConversation(context.Background(), "nonexistent",
		ConversationMessage{Role: "user", Message: "test"})
	if err == nil {
		t.Fatal("expected error for nonexistent plan")
	}
}

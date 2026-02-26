package engine

import (
	"testing"
)

func TestBuildDecompPrompt_ContainsTask(t *testing.T) {
	prompt := buildDecompPrompt("Add a health endpoint", "Go packages:\nmain")
	if len(prompt) == 0 {
		t.Fatal("prompt is empty")
	}
	if !contains(prompt, "Add a health endpoint") {
		t.Error("prompt should contain task description")
	}
	if !contains(prompt, "Go packages") {
		t.Error("prompt should contain codebase context")
	}
}

func TestDecompResult_EmptyStepsIsAtomic(t *testing.T) {
	r := DecompResult{Steps: nil}
	if len(r.Steps) != 0 {
		t.Error("expected no steps")
	}
}

func TestDecompResult_WithSteps(t *testing.T) {
	r := DecompResult{
		Steps: []DecompStep{
			{Title: "Step 1", Description: "Do thing 1", Acceptance: "Thing 1 done", Estimate: 15},
			{Title: "Step 2", Description: "Do thing 2", Acceptance: "Thing 2 done", Estimate: 30},
		},
	}
	if len(r.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(r.Steps))
	}
	if r.Steps[0].Estimate != 15 {
		t.Errorf("expected estimate 15, got %d", r.Steps[0].Estimate)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

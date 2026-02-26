package admit

import (
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/dag"
)

func validTask() dag.Task {
	return dag.Task{
		ID:              "test-001",
		Title:           "Add caching to Parser",
		Description:     "Add mtime-based caching to the Parser.ParseFile method to avoid re-parsing unchanged files.",
		Acceptance:      "ParseFile returns cached result when file mtime hasn't changed.",
		EstimateMinutes: 15,
		Type:            "task",
		Project:         "chum",
	}
}

func TestValidateStructure(t *testing.T) {
	tests := []struct {
		name   string
		modify func(dag.Task) dag.Task
		pass   bool
		reason string
	}{
		{
			name:   "valid task passes",
			modify: func(t dag.Task) dag.Task { return t },
			pass:   true,
		},
		{
			name:   "epic fails",
			modify: func(t dag.Task) dag.Task { t.Type = "epic"; return t },
			reason: "epics are containers",
		},
		{
			name:   "short description fails",
			modify: func(t dag.Task) dag.Task { t.Description = "Fix the bug."; return t },
			reason: "too short",
		},
		{
			name:   "description restating title fails",
			modify: func(t dag.Task) dag.Task {
				t.Title = "Add mtime-based caching to the Parser ParseFile method"
				t.Description = "Add mtime-based caching to the Parser ParseFile method."
				return t
			},
			reason: "detail beyond the title",
		},
		{
			name:   "empty acceptance fails",
			modify: func(t dag.Task) dag.Task { t.Acceptance = ""; return t },
			reason: "acceptance criteria",
		},
		{
			name:   "zero estimate fails",
			modify: func(t dag.Task) dag.Task { t.EstimateMinutes = 0; return t },
			reason: "estimate_minutes must be set",
		},
		{
			name:   "over 30 min fails",
			modify: func(t dag.Task) dag.Task { t.EstimateMinutes = 45; return t },
			reason: "broken down",
		},
		{
			name:   "exactly 30 min passes",
			modify: func(t dag.Task) dag.Task { t.EstimateMinutes = 30; return t },
			pass:   true,
		},
		{
			name: "description exactly 50 chars passes",
			modify: func(t dag.Task) dag.Task {
				t.Description = "This is exactly fifty characters long, check it!.."
				return t
			},
			pass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := tt.modify(validTask())
			result := ValidateStructure(task)
			if result.Pass != tt.pass {
				t.Errorf("Pass = %v, want %v (reason: %s)", result.Pass, tt.pass, result.Reason)
			}
			if !tt.pass && tt.reason != "" {
				if result.Reason == "" || !contains(result.Reason, tt.reason) {
					t.Errorf("Reason = %q, want it to contain %q", result.Reason, tt.reason)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsLower(s, substr))
}

func containsLower(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

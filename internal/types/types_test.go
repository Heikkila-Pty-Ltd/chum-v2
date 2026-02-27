package types

import "testing"

func TestTruncate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"over limit", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"zero limit", "hello", 0, "..."},
		{"unicode", "héllo wörld", 5, "héllo..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestStatusConstants(t *testing.T) {
	t.Parallel()
	statuses := []string{
		StatusOpen, StatusReady, StatusRunning, StatusCompleted,
		StatusFailed, StatusDecomposed, StatusDoDFailed,
		StatusNeedsRefinement, StatusStale,
	}
	seen := make(map[string]bool)
	for _, s := range statuses {
		if s == "" {
			t.Fatal("empty status constant")
		}
		if seen[s] {
			t.Fatalf("duplicate status constant: %s", s)
		}
		seen[s] = true
	}
}

func TestDecompStepFields(t *testing.T) {
	t.Parallel()
	step := DecompStep{
		Title:       "Test",
		Description: "A test step",
		Acceptance:  "It works",
		Estimate:    30,
	}
	if step.Title != "Test" {
		t.Errorf("unexpected title: %s", step.Title)
	}
	if step.Estimate != 30 {
		t.Errorf("unexpected estimate: %d", step.Estimate)
	}
}

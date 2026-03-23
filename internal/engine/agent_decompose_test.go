package engine

import (
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

func TestFlattenDecomposedSteps(t *testing.T) {
	tests := []struct {
		name     string
		steps    []types.DecompStep
		wantLen  int
		wantMax  int
	}{
		{
			name: "All under threshold",
			steps: []types.DecompStep{
				{Title: "Small task", Description: "Fix bug", Estimate: 10},
				{Title: "Another small", Description: "Add field", Estimate: 15},
			},
			wantLen: 2,
			wantMax: 15,
		},
		{
			name: "One oversized task splits into three",
			steps: []types.DecompStep{
				{Title: "Big task", Description: "Implement feature A. Then implement feature B. Finally implement feature C.", Estimate: 45},
			},
			wantLen: 3,
			wantMax: 15,
		},
		{
			name: "Mixed sizes",
			steps: []types.DecompStep{
				{Title: "Small", Description: "Fix typo", Estimate: 5},
				{Title: "Medium", Description: "Add test", Estimate: 30},
				{Title: "Large", Description: "Refactor module", Estimate: 60},
			},
			wantLen: 7,
			wantMax: 15,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flattenDecomposedSteps(tt.steps)
			if len(got) != tt.wantLen {
				t.Errorf("flattenDecomposedSteps() len = %v, want %v", len(got), tt.wantLen)
			}
			for _, s := range got {
				if s.Estimate > tt.wantMax {
					t.Errorf("step %q has estimate %v, want <= %v", s.Title, s.Estimate, tt.wantMax)
				}
			}
		})
	}
}

func TestSplitDescription(t *testing.T) {
	tests := []struct {
		name string
		desc string
		n    int
	}{
		{
			name: "Split into two",
			desc: "First part of the description. Second part of the description.",
			n:    2,
		},
		{
			name: "Split into three",
			desc: "Part one. Part two. Part three.",
			n:    3,
		},
		{
			name: "Don't split single sentence",
			desc: "This is a single sentence description.",
			n:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitDescription(tt.desc, tt.n)
			if len(got) != tt.n {
				t.Errorf("splitDescription() len = %v, want %v", len(got), tt.n)
			}
			joined := strings.Join(got, ". ")
			if joined != tt.desc {
				t.Errorf("splitDescription() content mismatch\ngot:  %q\nwant: %q", joined, tt.desc)
			}
		})
	}
}

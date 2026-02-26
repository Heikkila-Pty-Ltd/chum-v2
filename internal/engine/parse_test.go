package engine

import (
	"strings"
	"testing"
)

func TestExtractJSONVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     string
		contains  []string
		expectNil bool
	}{
		{
			name:     "raw object",
			input:    `{"summary":"ok","steps":["a"]}`,
			contains: []string{`"summary":"ok"`, `"steps":["a"]`},
		},
		{
			name:     "markdown code fence",
			input:    "```json\n{\"summary\":\"ok\",\"steps\":[\"a\"]}\n```",
			contains: []string{`"summary":"ok"`},
		},
		{
			name: "claude envelope",
			input: `{"type":"result","result":[{"type":"text","text":"{\"summary\":\"ok\",\"steps\":[\"a\"]}"}]}`,
			contains: []string{
				`"summary":"ok"`,
				`"steps":["a"]`,
			},
		},
		{
			name:     "commentary around json",
			input:    "Here is the JSON:\n{\"summary\":\"ok\",\"steps\":[\"a\"]}\nDone.",
			contains: []string{`"summary":"ok"`},
		},
		{
			name:     "trailing comma repaired",
			input:    `{"summary":"ok","steps":["a"],}`,
			contains: []string{`"summary":"ok"`},
		},
		{
			name:     "single quotes repaired",
			input:    "{'summary':'ok','steps':['a']}",
			contains: []string{`"summary":"ok"`},
		},
		{
			name:     "brace in string",
			input:    `{"summary":"ok } still ok","steps":["a"]}`,
			contains: []string{`"summary":"ok } still ok"`},
		},
		{
			name:      "no json object",
			input:     "nothing useful here",
			expectNil: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractJSON(tc.input)
			if tc.expectNil {
				if got != "" {
					t.Fatalf("ExtractJSON(%q) = %q, want empty", tc.input, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("ExtractJSON(%q) returned empty", tc.input)
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("ExtractJSON output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestNormalizePlan(t *testing.T) {
	t.Parallel()

	t.Run("fills summary from steps", func(t *testing.T) {
		p := &Plan{
			Steps: []string{"Implement parser hardening"},
		}
		NormalizePlan(p, "")
		if p.Summary == "" {
			t.Fatal("expected summary to be synthesized from steps")
		}
	})

	t.Run("fills summary from task prompt", func(t *testing.T) {
		p := &Plan{}
		NormalizePlan(p, "Fix flaky dispatcher tests\nwith details")
		if p.Summary == "" {
			t.Fatal("expected summary from task prompt")
		}
		if !strings.Contains(p.Summary, "Fix flaky dispatcher tests") {
			t.Fatalf("unexpected summary: %q", p.Summary)
		}
	})

	t.Run("synthesizes step from summary", func(t *testing.T) {
		p := &Plan{Summary: "Do thing"}
		NormalizePlan(p, "")
		if len(p.Steps) != 1 || p.Steps[0] != "Do thing" {
			t.Fatalf("steps = %v, want [Do thing]", p.Steps)
		}
	})

	t.Run("deduplicates and trims files", func(t *testing.T) {
		p := &Plan{
			FilesToModify: []string{" internal/a.go ", "internal/a.go", "internal/b.go"},
		}
		NormalizePlan(p, "")
		if len(p.FilesToModify) != 2 {
			t.Fatalf("files = %v, want 2 unique entries", p.FilesToModify)
		}
		if p.FilesToModify[0] != "internal/a.go" || p.FilesToModify[1] != "internal/b.go" {
			t.Fatalf("unexpected files after normalization: %v", p.FilesToModify)
		}
	})
}


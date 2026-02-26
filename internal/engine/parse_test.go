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

package jsonutil

import (
	"strings"
	"testing"
)

func TestFindObject(t *testing.T) {
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
			name:     "commentary around json",
			input:    "Here is the JSON:\n{\"summary\":\"ok\",\"steps\":[\"a\"]}\nDone.",
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
			got := FindObject(tc.input)
			if tc.expectNil {
				if got != "" {
					t.Fatalf("FindObject(%q) = %q, want empty", tc.input, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("FindObject(%q) returned empty", tc.input)
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("FindObject output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestExtractJSON_Object(t *testing.T) {
	t.Parallel()

	input := []byte(`some noise {"key":"value"} more noise`)
	got := ExtractJSON(input)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(string(got), `"key":"value"`) {
		t.Fatalf("unexpected result: %s", got)
	}
}

func TestExtractJSON_Array(t *testing.T) {
	t.Parallel()

	input := []byte(`noise [{"id":1},{"id":2}] trailing`)
	got := ExtractJSON(input)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(string(got), `"id":1`) {
		t.Fatalf("unexpected result: %s", got)
	}
}

func TestExtractJSON_AlreadyValid(t *testing.T) {
	t.Parallel()

	input := []byte(`[{"id":1}]`)
	got := ExtractJSON(input)
	if string(got) != `[{"id":1}]` {
		t.Fatalf("expected passthrough, got %q", got)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	t.Parallel()

	got := ExtractJSON([]byte("no json here"))
	if got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestFindBalancedEnd_Simple(t *testing.T) {
	t.Parallel()

	s := `{"key":"value"}`
	end := FindBalancedEnd(s, 0, '{', '}')
	if end != len(s)-1 {
		t.Fatalf("end = %d, want %d", end, len(s)-1)
	}
}

func TestFindBalancedEnd_Nested(t *testing.T) {
	t.Parallel()

	s := `{"a":{"b":"c"}}`
	end := FindBalancedEnd(s, 0, '{', '}')
	if end != len(s)-1 {
		t.Fatalf("end = %d, want %d", end, len(s)-1)
	}
}

func TestFindBalancedEnd_StringEscape(t *testing.T) {
	t.Parallel()

	s := `{"a":"val}ue"}`
	end := FindBalancedEnd(s, 0, '{', '}')
	if end != len(s)-1 {
		t.Fatalf("end = %d, want %d", end, len(s)-1)
	}
}

func TestFindBalancedEnd_NoClose(t *testing.T) {
	t.Parallel()

	s := `{"a":"b"`
	end := FindBalancedEnd(s, 0, '{', '}')
	if end != -1 {
		t.Fatalf("end = %d, want -1", end)
	}
}

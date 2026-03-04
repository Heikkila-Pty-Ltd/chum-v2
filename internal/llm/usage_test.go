package llm

import "testing"

func TestParseCLIUsage_ClaudeEnvelope(t *testing.T) {
	t.Parallel()
	output := `{"type":"result","result":"done","usage":{"input_tokens":1500,"output_tokens":300}}`
	stats := ParseCLIUsage(output, "claude")
	if stats.InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500", stats.InputTokens)
	}
	if stats.OutputTokens != 300 {
		t.Errorf("OutputTokens = %d, want 300", stats.OutputTokens)
	}
}

func TestParseCLIUsage_ClaudeStreaming(t *testing.T) {
	t.Parallel()
	// Streaming output has multiple JSON objects; usage is in a later event.
	output := `{"type":"content","text":"hello"}
{"type":"usage","input_tokens":2000,"output_tokens":500}
{"type":"result","result":"done"}`
	stats := ParseCLIUsage(output, "claude")
	if stats.InputTokens != 2000 {
		t.Errorf("InputTokens = %d, want 2000", stats.InputTokens)
	}
	if stats.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", stats.OutputTokens)
	}
}

func TestParseCLIUsage_GeminiTextSummary(t *testing.T) {
	t.Parallel()
	output := "Some output here\nTokens: 1,234 input, 567 output\nDone."
	stats := ParseCLIUsage(output, "gemini")
	if stats.InputTokens != 1234 {
		t.Errorf("InputTokens = %d, want 1234", stats.InputTokens)
	}
	if stats.OutputTokens != 567 {
		t.Errorf("OutputTokens = %d, want 567", stats.OutputTokens)
	}
}

func TestParseCLIUsage_GeminiPromptCandidates(t *testing.T) {
	t.Parallel()
	output := "Model response\nprompt=800, candidates=200\n"
	stats := ParseCLIUsage(output, "gemini")
	if stats.InputTokens != 800 {
		t.Errorf("InputTokens = %d, want 800", stats.InputTokens)
	}
	if stats.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", stats.OutputTokens)
	}
}

func TestParseCLIUsage_CodexJSON(t *testing.T) {
	t.Parallel()
	output := `some text
{"tokens_in":900,"tokens_out":150}
more text`
	stats := ParseCLIUsage(output, "codex")
	if stats.InputTokens != 900 {
		t.Errorf("InputTokens = %d, want 900", stats.InputTokens)
	}
	if stats.OutputTokens != 150 {
		t.Errorf("OutputTokens = %d, want 150", stats.OutputTokens)
	}
}

func TestParseCLIUsage_UnknownAgent(t *testing.T) {
	t.Parallel()
	// Unknown agent with no detectable usage patterns → zeros.
	stats := ParseCLIUsage("just regular output", "unknown-agent")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 || stats.CostUSD != 0 {
		t.Errorf("expected all zeros for unknown agent, got %+v", stats)
	}
}

func TestParseCLIUsage_UnknownAgentWithClaudeData(t *testing.T) {
	t.Parallel()
	// Unknown agent but output contains Claude-format data → detected.
	output := `{"type":"result","usage":{"input_tokens":100,"output_tokens":50}}`
	stats := ParseCLIUsage(output, "unknown")
	if stats.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", stats.InputTokens)
	}
}

func TestParseCLIUsage_EmptyOutput(t *testing.T) {
	t.Parallel()
	stats := ParseCLIUsage("", "claude")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("expected zeros for empty output, got %+v", stats)
	}
}

func TestFindAllJSONObjects(t *testing.T) {
	t.Parallel()
	input := `text {"a":1} more {"b":2} end`
	objects := findAllJSONObjects(input)
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
	if objects[0] != `{"a":1}` {
		t.Errorf("objects[0] = %q, want %q", objects[0], `{"a":1}`)
	}
	if objects[1] != `{"b":2}` {
		t.Errorf("objects[1] = %q, want %q", objects[1], `{"b":2}`)
	}
}

func TestParseIntCommas(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"1234", 1234},
		{"1,234", 1234},
		{"1,234,567", 1234567},
		{"0", 0},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseIntCommas(tt.input); got != tt.want {
			t.Errorf("parseIntCommas(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

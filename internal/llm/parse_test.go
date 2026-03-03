package llm

import (
	"testing"
)

func TestExtractJSON_RawObject(t *testing.T) {
	t.Parallel()
	input := `{"key": "value"}`
	got := ExtractJSON(input)
	if got != `{"key": "value"}` {
		t.Errorf("expected raw object, got %s", got)
	}
}

func TestExtractJSON_RawArray(t *testing.T) {
	t.Parallel()
	input := `[{"a":1},{"b":2}]`
	got := ExtractJSON(input)
	if got != input {
		t.Errorf("expected raw array, got %s", got)
	}
}

func TestExtractJSON_CodeFenceWrapped(t *testing.T) {
	t.Parallel()
	input := "```json\n{\"key\": \"value\"}\n```"
	got := ExtractJSON(input)
	if got != `{"key": "value"}` {
		t.Errorf("expected unwrapped object, got %s", got)
	}
}

func TestExtractJSON_CodeFenceJsonc(t *testing.T) {
	t.Parallel()
	input := "```jsonc\n{\"key\": \"value\"}\n```"
	got := ExtractJSON(input)
	if got != `{"key": "value"}` {
		t.Errorf("expected unwrapped object, got %s", got)
	}
}

func TestExtractJSON_SurroundingCommentary(t *testing.T) {
	t.Parallel()
	input := "Sure! Here is the result:\n{\"steps\": [{\"title\": \"step1\"}]}\nHope that helps!"
	got := ExtractJSON(input)
	if got != `{"steps": [{"title": "step1"}]}` {
		t.Errorf("got %s", got)
	}
}

func TestExtractJSON_Empty(t *testing.T) {
	t.Parallel()
	if got := ExtractJSON(""); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	t.Parallel()
	if got := ExtractJSON("just some plain text with no JSON at all"); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestExtractJSON_ArrayBeforeObject(t *testing.T) {
	t.Parallel()
	input := `Here: [1,2,3] and also {"a":1}`
	got := ExtractJSON(input)
	if got != "[1,2,3]" {
		t.Errorf("expected array (appears first), got %s", got)
	}
}

func TestExtractJSON_ObjectBeforeArray(t *testing.T) {
	t.Parallel()
	input := `Result: {"a":1} and [1,2]`
	got := ExtractJSON(input)
	if got != `{"a":1}` {
		t.Errorf("expected object (appears first), got %s", got)
	}
}

func TestExtractJSON_TrailingComma(t *testing.T) {
	t.Parallel()
	input := `{"key": "value",}`
	got := ExtractJSON(input)
	if got != `{"key": "value"}` {
		t.Errorf("expected repaired JSON, got %s", got)
	}
}

func TestExtractJSON_SingleQuotes(t *testing.T) {
	t.Parallel()
	input := `{'key': 'value'}`
	got := ExtractJSON(input)
	if got != `{"key": "value"}` {
		t.Errorf("expected repaired JSON with double quotes, got %s", got)
	}
}

func TestExtractJSON_HereIsTheJSON(t *testing.T) {
	t.Parallel()
	input := "Here is the JSON output:\n{\"key\": \"value\"}"
	got := ExtractJSON(input)
	if got != `{"key": "value"}` {
		t.Errorf("expected extracted object, got %s", got)
	}
}

func TestExtractJSON_NestedObject(t *testing.T) {
	t.Parallel()
	input := `{"outer": {"inner": [1,2,3]}}`
	got := ExtractJSON(input)
	if got != input {
		t.Errorf("expected nested object preserved, got %s", got)
	}
}

func TestExtractJSON_BracesInString(t *testing.T) {
	t.Parallel()
	input := `{"msg": "use {braces} in text"}`
	got := ExtractJSON(input)
	if got != input {
		t.Errorf("expected braces in strings ignored, got %s", got)
	}
}

func TestUnwrapClaudeJSON_StringResult(t *testing.T) {
	t.Parallel()
	input := `{"type":"result","result":"{\"key\":\"value\"}"}`
	got := UnwrapClaudeJSON(input)
	if got != `{"key":"value"}` {
		t.Errorf("expected unwrapped string result, got %s", got)
	}
}

func TestUnwrapClaudeJSON_ArrayResult(t *testing.T) {
	t.Parallel()
	input := `{"type":"result","result":[{"type":"text","text":"hello world"}]}`
	got := UnwrapClaudeJSON(input)
	if got != "hello world" {
		t.Errorf("expected unwrapped array result, got %s", got)
	}
}

func TestUnwrapClaudeJSON_MultipleTextBlocks(t *testing.T) {
	t.Parallel()
	input := `{"type":"result","result":[{"type":"text","text":"part1"},{"type":"text","text":"part2"}]}`
	got := UnwrapClaudeJSON(input)
	if got != "part1\npart2" {
		t.Errorf("expected joined text blocks, got %s", got)
	}
}

func TestUnwrapClaudeJSON_NotClaudeEnvelope(t *testing.T) {
	t.Parallel()
	input := `{"key": "value"}`
	got := UnwrapClaudeJSON(input)
	if got != input {
		t.Errorf("expected passthrough, got %s", got)
	}
}

func TestUnwrapClaudeJSON_PlainText(t *testing.T) {
	t.Parallel()
	input := "just some text"
	got := UnwrapClaudeJSON(input)
	if got != input {
		t.Errorf("expected passthrough, got %s", got)
	}
}

func TestUnwrapClaudeJSON_EmptyResult(t *testing.T) {
	t.Parallel()
	input := `{"type":"result","result":""}`
	got := UnwrapClaudeJSON(input)
	// Empty result string — should fall through to original text.
	if got != input {
		t.Errorf("expected passthrough for empty result, got %s", got)
	}
}

func TestUnwrapClaudeJSON_ArrayResultNoText(t *testing.T) {
	t.Parallel()
	input := `{"type":"result","result":[{"type":"image","text":""}]}`
	got := UnwrapClaudeJSON(input)
	// No text blocks — should fall through to original.
	if got != input {
		t.Errorf("expected passthrough for non-text array result, got %s", got)
	}
}

func TestExtractJSON_ClaudeEnvelopeWithCodeFence(t *testing.T) {
	t.Parallel()
	input := `{"type":"result","result":"` + "```json\\n{\\\"steps\\\": []}\\n```" + `"}`
	got := ExtractJSON(input)
	if got != `{"steps": []}` {
		t.Errorf("expected extracted inner JSON, got %s", got)
	}
}

func TestRepairJSON_ValidPassthrough(t *testing.T) {
	t.Parallel()
	input := `{"valid": true}`
	got := repairJSON(input)
	if got != input {
		t.Errorf("expected passthrough, got %s", got)
	}
}

func TestRepairJSON_Unrepairable(t *testing.T) {
	t.Parallel()
	got := repairJSON(`{totally broken`)
	if got != "" {
		t.Errorf("expected empty for unrepairable, got %s", got)
	}
}

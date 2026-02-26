package llm

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/jsonutil"
)

// ExtractJSON finds the first JSON object or array in LLM output.
// Handles common LLM wrapping patterns: raw JSON, code fences,
// Claude's --output-format json envelope, and surrounding commentary.
func ExtractJSON(text string) string {
	text = UnwrapClaudeJSON(text)
	text = stripCodeFences(text)
	text = cleanLLMArtifacts(text)

	// Try object first, then array, return whichever appears first in the text.
	obj := findJSONObject(text)
	arr := findJSONArray(text)
	switch {
	case obj == "" && arr == "":
		return ""
	case obj == "":
		return arr
	case arr == "":
		return obj
	default:
		// Return whichever appears first in the cleaned text.
		if strings.Index(text, arr) < strings.Index(text, obj) {
			return arr
		}
		return obj
	}
}

// UnwrapClaudeJSON handles Claude's --output-format json envelope.
func UnwrapClaudeJSON(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") || !strings.Contains(text, `"type"`) {
		return text
	}
	var envelope struct {
		Type   string `json:"type"`
		Result []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return text
	}
	if envelope.Type != "result" || len(envelope.Result) == 0 {
		return text
	}
	var parts []string
	for _, r := range envelope.Result {
		if r.Type == "text" && r.Text != "" {
			parts = append(parts, r.Text)
		}
	}
	if len(parts) == 0 {
		return text
	}
	return strings.Join(parts, "\n")
}

var codeFencePattern = regexp.MustCompile("(?i)```(?:json[c]?)?\\s*\n?")
var trailingCommaPattern = regexp.MustCompile(`,\s*([}\]])`)

func stripCodeFences(text string) string {
	return codeFencePattern.ReplaceAllString(text, "")
}

func cleanLLMArtifacts(text string) string {
	prefixes := []string{
		"here is the json",
		"here's the json",
		"here is my plan",
		"here's my plan",
		"json output:",
	}
	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			rest := text[idx:]
			if nlIdx := strings.IndexByte(rest, '\n'); nlIdx >= 0 {
				text = text[:idx] + rest[nlIdx:]
			}
		}
	}
	return text
}

func findJSONObject(text string) string {
	if obj := jsonutil.FindObject(text); obj != "" {
		return obj
	}
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString || ch != '{' {
			continue
		}
		if end := jsonutil.FindBalancedEnd(text, i, '{', '}'); end > i {
			candidate := text[i : end+1]
			if repaired := repairJSON(candidate); repaired != "" {
				return repaired
			}
		}
	}
	return ""
}

func findJSONArray(text string) string {
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString || ch != '[' {
			continue
		}
		if end := jsonutil.FindBalancedEnd(text, i, '[', ']'); end > i {
			candidate := text[i : end+1]
			if repaired := repairJSON(candidate); repaired != "" {
				return repaired
			}
		}
	}
	return ""
}

func repairJSON(text string) string {
	text = trailingCommaPattern.ReplaceAllString(text, "$1")
	if !json.Valid([]byte(text)) {
		attempt := strings.ReplaceAll(text, "'", "\"")
		if json.Valid([]byte(attempt)) {
			return attempt
		}
	}
	if json.Valid([]byte(text)) {
		return text
	}
	return ""
}

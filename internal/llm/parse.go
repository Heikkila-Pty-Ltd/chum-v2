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
// The CLI returns either:
//   - {"type":"result","result":"text content"}            (result as string)
//   - {"type":"result","result":[{"type":"text","text":"..."}]} (result as array)
func UnwrapClaudeJSON(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") || !strings.Contains(text, `"type"`) {
		return text
	}

	// Try result-as-string format first (Claude CLI --output-format json).
	var strEnvelope struct {
		Type   string `json:"type"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &strEnvelope); err == nil && strEnvelope.Type == "result" && strEnvelope.Result != "" {
		return strEnvelope.Result
	}

	// Try result-as-array format (Claude API style).
	var arrEnvelope struct {
		Type   string `json:"type"`
		Result []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &arrEnvelope); err == nil && arrEnvelope.Type == "result" && len(arrEnvelope.Result) > 0 {
		var parts []string
		for _, r := range arrEnvelope.Result {
			if r.Type == "text" && r.Text != "" {
				parts = append(parts, r.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}

	return text
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
	for _, prefix := range prefixes {
		lower := strings.ToLower(text)
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
	if json.Valid([]byte(text)) {
		return text
	}
	attempt := strings.ReplaceAll(text, "'", "\"")
	if json.Valid([]byte(attempt)) {
		return attempt
	}
	return ""
}

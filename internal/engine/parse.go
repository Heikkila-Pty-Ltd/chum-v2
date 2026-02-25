package engine

import (
	"encoding/json"
	"strings"
)

// ExtractJSON finds the first JSON object in text.
// Handles markdown code fences (```json ... ```).
// Returns empty string if no valid JSON object found.
func ExtractJSON(text string) string {
	// Strip markdown code fences first
	text = stripCodeFences(text)

	// Find the first { and match to its closing }
	depth := 0
	start := -1
	inString := false
	escaped := false

	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if r == '{' {
			if start == -1 {
				start = i
			}
			depth++
		} else if r == '}' {
			depth--
			if depth == 0 && start >= 0 {
				candidate := text[start : i+1]
				if json.Valid([]byte(candidate)) {
					return candidate
				}
				// Not valid — reset and try next
				start = -1
			}
		}
	}
	return ""
}

func stripCodeFences(text string) string {
	// Remove ```json and ``` markers
	text = strings.ReplaceAll(text, "```json", "")
	text = strings.ReplaceAll(text, "```", "")
	return text
}

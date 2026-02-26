package engine

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/jsonutil"
)

// ExtractJSON finds the first JSON object in LLM output.
// Handles all common LLM wrapping patterns:
//   - Raw JSON
//   - Markdown code fences (```json ... ``` or ``` ... ```)
//   - Claude's --output-format json ({"type":"result","result":[{"type":"text","text":"..."}]})
//   - Commentary before/after the JSON
//   - Newlines and whitespace inside JSON strings
//
// Returns empty string if no valid JSON object found.
func ExtractJSON(text string) string {
	// Step 1: Try to unwrap Claude's JSON protocol envelope
	text = unwrapClaudeJSON(text)

	// Step 2: Strip markdown code fences
	text = stripCodeFences(text)

	// Step 3: Clean up common LLM artifacts
	text = cleanLLMArtifacts(text)

	// Step 4: Find the first balanced JSON object
	return findJSONObject(text)
}

// unwrapClaudeJSON handles Claude's --output-format json envelope.
// Claude wraps its text output in: {"type":"result","result":[{"type":"text","text":"..."}]}
func unwrapClaudeJSON(text string) string {
	text = strings.TrimSpace(text)

	// Quick check — does this look like Claude's envelope?
	if !strings.HasPrefix(text, "{") || !strings.Contains(text, `"type"`) {
		return text
	}

	// Try to parse as Claude envelope
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

	// Concatenate all text blocks
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

// stripCodeFences removes markdown code block markers.
// Handles: ```json, ```JSON, ```jsonc, plain ```
var codeFencePattern = regexp.MustCompile("(?i)```(?:json[c]?)?\\s*\n?")

// trailingCommaPattern matches trailing commas before } or ] in JSON.
var trailingCommaPattern = regexp.MustCompile(`,\s*([}\]])`)

func stripCodeFences(text string) string {
	return codeFencePattern.ReplaceAllString(text, "")
}

// cleanLLMArtifacts removes common LLM commentary patterns that wrap JSON.
func cleanLLMArtifacts(text string) string {
	// Remove "Here is the JSON:" style prefixes
	prefixes := []string{
		"here is the json",
		"here's the json",
		"here is my plan",
		"here's my plan",
		"json output:",
		"plan:",
	}
	lower := strings.ToLower(text)
	for _, prefix := range prefixes {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			// Find the end of this line
			rest := text[idx:]
			if nlIdx := strings.IndexByte(rest, '\n'); nlIdx >= 0 {
				text = text[:idx] + rest[nlIdx:]
			}
		}
	}
	return text
}

// findJSONObject locates the first complete JSON object from text.
// Delegates to jsonutil.FindObject for bracket-balancing, then falls back
// to repairJSON if the first candidate isn't valid.
func findJSONObject(text string) string {
	if obj := jsonutil.FindObject(text); obj != "" {
		return obj
	}
	// jsonutil.FindObject only returns valid JSON; try repair on raw candidates.
	// Track string state so we skip braces inside quoted text in surrounding prose.
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

// repairJSON attempts to fix common JSON formatting issues from LLMs.
func repairJSON(text string) string {
	// Fix 1: trailing commas before } or ]
	text = trailingCommaPattern.ReplaceAllString(text, "$1")

	// Fix 2: single quotes → double quotes (only outside existing double quotes)
	// This is risky so only try if the result would be valid
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


package engine

import (
	"encoding/json"
	"regexp"
	"strings"
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

// findJSONObject locates and extracts the first complete JSON object from text.
func findJSONObject(text string) string {
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
				// Not valid — try to repair common issues
				if repaired := repairJSON(candidate); repaired != "" {
					return repaired
				}
				// Reset and try next
				start = -1
			}
		}
	}
	return ""
}

// repairJSON attempts to fix common JSON formatting issues from LLMs.
func repairJSON(text string) string {
	// Fix 1: trailing commas before } or ]
	trailingComma := regexp.MustCompile(`,\s*([}\]])`)
	text = trailingComma.ReplaceAllString(text, "$1")

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

// NormalizePlan takes a raw Plan and fills in missing fields with sensible defaults
// when the LLM provided partial data. This is the "ensure it fits" step.
func NormalizePlan(p *Plan, taskPrompt string) {
	// If summary is empty but we have steps, synthesize from first step
	if p.Summary == "" && len(p.Steps) > 0 {
		p.Summary = p.Steps[0]
		if len(p.Summary) > 120 {
			p.Summary = p.Summary[:120]
		}
	}

	// If summary is still empty, use the task prompt
	if p.Summary == "" && taskPrompt != "" {
		lines := strings.SplitN(taskPrompt, "\n", 2)
		p.Summary = strings.TrimSpace(lines[0])
		if len(p.Summary) > 120 {
			p.Summary = p.Summary[:120]
		}
	}

	// If no steps but we have a summary, create a single step from it
	if len(p.Steps) == 0 && p.Summary != "" {
		p.Steps = []string{p.Summary}
	}

	// Deduplicate files
	if len(p.FilesToModify) > 0 {
		seen := make(map[string]bool)
		unique := make([]string, 0, len(p.FilesToModify))
		for _, f := range p.FilesToModify {
			f = strings.TrimSpace(f)
			if f != "" && !seen[f] {
				seen[f] = true
				unique = append(unique, f)
			}
		}
		p.FilesToModify = unique
	}
}

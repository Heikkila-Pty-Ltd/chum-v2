// Package jsonutil provides shared JSON extraction utilities for parsing
// LLM output and CLI output that may contain JSON wrapped in commentary.
package jsonutil

import (
	"encoding/json"
	"strings"
)

// FindBalancedEnd returns the index of the closing bracket that balances
// the opener at position start, respecting JSON string escaping.
// Returns -1 if no balanced close is found.
func FindBalancedEnd(s string, start int, open, close byte) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
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
		if inString {
			continue
		}
		if ch == open {
			depth++
		} else if ch == close {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// FindObject locates and extracts the first complete JSON object ({...})
// from text, respecting string escaping. Returns empty string if none found.
func FindObject(text string) string {
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
				// Reset and try next object
				start = -1
			}
		}
	}
	return ""
}

// ExtractJSON finds the first balanced JSON object or array in text,
// tolerating leading/trailing non-JSON output (e.g. CLI stderr noise).
func ExtractJSON(text []byte) []byte {
	s := strings.TrimSpace(string(text))
	if json.Valid([]byte(s)) {
		return []byte(s)
	}
	for i := 0; i < len(s); i++ {
		open := s[i]
		var close byte
		switch open {
		case '{':
			close = '}'
		case '[':
			close = ']'
		default:
			continue
		}
		if end := FindBalancedEnd(s, i, open, close); end > i {
			candidate := s[i : end+1]
			if json.Valid([]byte(candidate)) {
				return []byte(candidate)
			}
		}
	}
	return nil
}

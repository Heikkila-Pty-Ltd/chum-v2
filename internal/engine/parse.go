package engine

import (
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
)

// ExtractJSON finds the first JSON object in LLM output.
// Delegates to the shared llm package.
func ExtractJSON(text string) string {
	return llm.ExtractJSON(text)
}

// unwrapClaudeJSON is used by review.go to strip Claude's JSON envelope.
func unwrapClaudeJSON(text string) string {
	return llm.UnwrapClaudeJSON(text)
}

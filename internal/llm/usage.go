package llm

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// UsageStats holds token usage and estimated cost extracted from CLI output.
type UsageStats struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// ParseCLIUsage extracts token usage from LLM CLI output.
// Best-effort: returns zero values if parsing fails — never errors.
func ParseCLIUsage(output, agent string) UsageStats {
	agent = strings.ToLower(agent)

	switch {
	case strings.HasPrefix(agent, "claude"):
		return parseClaudeUsage(output)
	case strings.HasPrefix(agent, "gemini"):
		return parseGeminiUsage(output)
	case strings.HasPrefix(agent, "codex"):
		return parseCodexUsage(output)
	default:
		// Try each parser in order; return first non-zero result.
		if s := parseClaudeUsage(output); s.InputTokens > 0 || s.OutputTokens > 0 {
			return s
		}
		if s := parseGeminiUsage(output); s.InputTokens > 0 || s.OutputTokens > 0 {
			return s
		}
		return UsageStats{}
	}
}

// parseClaudeUsage extracts usage from Claude's --output-format json envelope.
// Claude CLI outputs: {"type":"result","result":"...","usage":{"input_tokens":N,"output_tokens":N},...}
// It may also appear in streaming events with "type":"usage".
func parseClaudeUsage(output string) UsageStats {
	// Try the top-level result envelope first.
	var envelope struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	// The output may have multiple JSON objects (streaming); find the last one
	// with "usage" data by scanning all top-level JSON objects.
	var best UsageStats
	for _, candidate := range findAllJSONObjects(output) {
		if err := json.Unmarshal([]byte(candidate), &envelope); err == nil {
			if envelope.Usage.InputTokens > 0 || envelope.Usage.OutputTokens > 0 {
				best = UsageStats{
					InputTokens:  envelope.Usage.InputTokens,
					OutputTokens: envelope.Usage.OutputTokens,
				}
			}
		}
	}

	// Also check for the streaming "usage" event type.
	if best.InputTokens == 0 && best.OutputTokens == 0 {
		var usageEvent struct {
			Type         string `json:"type"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
		}
		for _, candidate := range findAllJSONObjects(output) {
			if err := json.Unmarshal([]byte(candidate), &usageEvent); err == nil &&
				usageEvent.Type == "usage" {
				best = UsageStats{
					InputTokens:  usageEvent.InputTokens,
					OutputTokens: usageEvent.OutputTokens,
				}
			}
		}
	}

	return best
}

// geminiUsagePattern matches lines like "Tokens: 1234 input, 567 output"
// or "Token count: prompt=1234, candidates=567"
var geminiUsagePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(\d[\d,]*)\s*input.*?(\d[\d,]*)\s*output`),
	regexp.MustCompile(`(?i)prompt\s*[=:]\s*(\d[\d,]*).*?candidates?\s*[=:]\s*(\d[\d,]*)`),
	regexp.MustCompile(`(?i)input.tokens?\s*[=:]\s*(\d[\d,]*).*?output.tokens?\s*[=:]\s*(\d[\d,]*)`),
}

func parseGeminiUsage(output string) UsageStats {
	for _, pat := range geminiUsagePatterns {
		if m := pat.FindStringSubmatch(output); len(m) >= 3 {
			input := parseIntCommas(m[1])
			outputTok := parseIntCommas(m[2])
			if input > 0 || outputTok > 0 {
				return UsageStats{InputTokens: input, OutputTokens: outputTok}
			}
		}
	}
	return UsageStats{}
}

// parseCodexUsage extracts usage from Codex CLI output.
// Codex may output a summary JSON or text line with token counts.
func parseCodexUsage(output string) UsageStats {
	// Try JSON format first.
	var stats struct {
		TokensIn  int `json:"tokens_in"`
		TokensOut int `json:"tokens_out"`
	}
	for _, candidate := range findAllJSONObjects(output) {
		if err := json.Unmarshal([]byte(candidate), &stats); err == nil {
			if stats.TokensIn > 0 || stats.TokensOut > 0 {
				return UsageStats{InputTokens: stats.TokensIn, OutputTokens: stats.TokensOut}
			}
		}
	}

	// Fall back to text patterns.
	return parseGeminiUsage(output) // reuse generic patterns
}

// findAllJSONObjects finds all top-level JSON objects in text.
func findAllJSONObjects(text string) []string {
	var results []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		depth := 0
		inString := false
		escaped := false
		for j := i; j < len(text); j++ {
			ch := text[j]
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
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					results = append(results, text[i:j+1])
					i = j
					break
				}
			}
		}
	}
	return results
}

// parseIntCommas parses an integer that may contain commas (e.g. "1,234").
func parseIntCommas(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	n, _ := strconv.Atoi(s)
	return n
}

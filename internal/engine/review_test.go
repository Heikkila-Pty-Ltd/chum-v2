package engine

import (
	"strings"
	"testing"
)

func TestParseReviewSignal_UnwrapsClaudeEnvelope(t *testing.T) {
	t.Parallel()

	output := `{"type":"result","result":[{"type":"text","text":"APPROVE\nLooks good to me."}]}`
	signal, body, invalid := parseReviewSignal(output)
	if invalid {
		t.Fatal("expected valid signal after envelope unwrap")
	}
	if signal != "APPROVE" {
		t.Fatalf("signal = %q, want APPROVE", signal)
	}
	if body != "Looks good to me." {
		t.Fatalf("body = %q, want reviewer rationale", body)
	}
}

func TestParseReviewSignal_InvalidDefaultsToRequestChanges(t *testing.T) {
	t.Parallel()

	signal, body, invalid := parseReviewSignal("LGTM\nship it")
	if !invalid {
		t.Fatal("expected invalid=true for unsupported signal")
	}
	if signal != "REQUEST_CHANGES" {
		t.Fatalf("signal = %q, want REQUEST_CHANGES", signal)
	}
	if body == "" {
		t.Fatal("expected non-empty fallback body")
	}
}

func TestDefaultReviewer(t *testing.T) {
	t.Parallel()

	if got := DefaultReviewer("claude"); got != "gemini" {
		t.Fatalf("DefaultReviewer(claude) = %q, want gemini", got)
	}
	if got := DefaultReviewer("gemini"); got != "claude" {
		t.Fatalf("DefaultReviewer(gemini) = %q, want claude", got)
	}
	if got := DefaultReviewer("codex"); got != "claude" {
		t.Fatalf("DefaultReviewer(codex) = %q, want claude", got)
	}
}

func TestBuildReviewPrompt_ContainsPRNumber(t *testing.T) {
	t.Parallel()

	prNumber := 42
	round := 1
	diff := "some diff content"

	prompt := buildReviewPrompt(prNumber, round, diff)

	if !containsString(prompt, "#42") {
		t.Fatalf("expected prompt to contain PR number #42, got: %s", prompt)
	}
}

func TestBuildReviewPrompt_ContainsRoundNumber(t *testing.T) {
	t.Parallel()

	prNumber := 1
	round := 3
	diff := "some diff content"

	prompt := buildReviewPrompt(prNumber, round, diff)

	if !containsString(prompt, "Review round: 3") {
		t.Fatalf("expected prompt to contain round number 3, got: %s", prompt)
	}
}

func TestBuildReviewPrompt_ContainsDiffText(t *testing.T) {
	t.Parallel()

	prNumber := 1
	round := 1
	diff := "unique diff content xyz123"

	prompt := buildReviewPrompt(prNumber, round, diff)

	if !containsString(prompt, diff) {
		t.Fatalf("expected prompt to contain diff text %q, got: %s", diff, prompt)
	}
}

func TestCapDiff_UnderLimit(t *testing.T) {
	t.Parallel()

	input := "small diff content"
	result := capDiff(input)

	if result != input {
		t.Fatalf("expected unchanged input for small diff, got: %s", result)
	}
}

func TestCapDiff_ExactlyAtLimit(t *testing.T) {
	t.Parallel()

	// Create a string exactly 120000 bytes
	input := strings.Repeat("a", 120000)
	result := capDiff(input)

	if result != input {
		t.Fatalf("expected unchanged input for diff exactly at limit")
	}
	if len(result) != 120000 {
		t.Fatalf("expected result to be exactly 120000 bytes, got %d", len(result))
	}
}

func TestCapDiff_OverLimit(t *testing.T) {
	t.Parallel()

	// Create a string over 120000 bytes
	input := strings.Repeat("b", 120001)
	result := capDiff(input)

	expectedTruncation := "\n\n[truncated by CHUM]"
	if !strings.HasSuffix(result, expectedTruncation) {
		t.Fatalf("expected result to end with truncation marker, got: %s", result[len(result)-30:])
	}

	// Should be exactly 120000 + length of truncation marker
	expectedLength := 120000 + len(expectedTruncation)
	if len(result) != expectedLength {
		t.Fatalf("expected result length %d, got %d", expectedLength, len(result))
	}
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}

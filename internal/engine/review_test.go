package engine

import "testing"

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

func TestParseReviewSignal_EmptyInput(t *testing.T) {
	t.Parallel()

	signal, body, invalid := parseReviewSignal("")
	if !invalid {
		t.Fatal("expected invalid=true for empty input")
	}
	if signal != "REQUEST_CHANGES" {
		t.Fatalf("signal = %q, want REQUEST_CHANGES", signal)
	}
	if body != "Invalid reviewer output: empty response." {
		t.Fatalf("body = %q, want empty response message", body)
	}
}

func TestParseReviewSignal_WhitespaceOnlyInput(t *testing.T) {
	t.Parallel()

	signal, body, invalid := parseReviewSignal("   \n\t  \n  ")
	if !invalid {
		t.Fatal("expected invalid=true for whitespace-only input")
	}
	if signal != "REQUEST_CHANGES" {
		t.Fatalf("signal = %q, want REQUEST_CHANGES", signal)
	}
	if body != "Invalid reviewer output: empty response." {
		t.Fatalf("body = %q, want empty response message", body)
	}
}

func TestParseReviewSignal_ApproveWithNoBody(t *testing.T) {
	t.Parallel()

	signal, body, invalid := parseReviewSignal("APPROVE")
	if invalid {
		t.Fatal("expected invalid=false for valid APPROVE signal")
	}
	if signal != "APPROVE" {
		t.Fatalf("signal = %q, want APPROVE", signal)
	}
	if body != "Approved." {
		t.Fatalf("body = %q, want default approved message", body)
	}
}

func TestParseReviewSignal_RequestChangesWithMultiLineBody(t *testing.T) {
	t.Parallel()

	input := `REQUEST_CHANGES
Please fix the following issues:
1. Add proper error handling
2. Update documentation
3. Fix memory leak in line 42

These changes are required before merging.`

	signal, body, invalid := parseReviewSignal(input)
	if invalid {
		t.Fatal("expected invalid=false for valid REQUEST_CHANGES signal")
	}
	if signal != "REQUEST_CHANGES" {
		t.Fatalf("signal = %q, want REQUEST_CHANGES", signal)
	}

	expectedBody := `Please fix the following issues:
1. Add proper error handling
2. Update documentation
3. Fix memory leak in line 42

These changes are required before merging.`
	if body != expectedBody {
		t.Fatalf("body = %q, want multi-line body", body)
	}
}

func TestParseReviewSignal_SignalWithLeadingWhitespace(t *testing.T) {
	t.Parallel()

	input := `

   APPROVE
Code looks good to go.`

	signal, body, invalid := parseReviewSignal(input)
	if invalid {
		t.Fatal("expected invalid=false for valid signal with leading whitespace")
	}
	if signal != "APPROVE" {
		t.Fatalf("signal = %q, want APPROVE", signal)
	}
	if body != "Code looks good to go." {
		t.Fatalf("body = %q, want trimmed body", body)
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

func TestFindLatestMatchingReview_EmptyReviewsList(t *testing.T) {
	t.Parallel()

	var reviews []ghReview
	_, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if found {
		t.Fatal("expected no match for empty reviews list")
	}
}

func TestFindLatestMatchingReview_NoMatchingReviewer(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "APPROVED",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "otheruser"},
		},
	}
	_, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if found {
		t.Fatal("expected no match when reviewer doesn't match")
	}
}

func TestFindLatestMatchingReview_MatchingReviewerWrongSHA(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "APPROVED",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "wrongsha",
			User:     ghUser{Login: "testuser"},
		},
	}
	_, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if found {
		t.Fatal("expected no match when SHA doesn't match")
	}
}

func TestFindLatestMatchingReview_MatchingReviewerSHAButDismissed(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "DISMISSED",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "testuser"},
		},
	}
	_, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if found {
		t.Fatal("expected no match when review is dismissed")
	}
}

func TestFindLatestMatchingReview_MatchingReviewerSHARoundMarkerFound(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "APPROVED",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "testuser"},
		},
	}
	review, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if !found {
		t.Fatal("expected match when all criteria are met")
	}
	if review.ID != 1 {
		t.Fatalf("expected review ID 1, got %d", review.ID)
	}
}

func TestFindLatestMatchingReview_MultipleReviewsPicksLatest(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "APPROVED",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "testuser"},
		},
		{
			ID:       2,
			State:    "CHANGES_REQUESTED",
			Body:     "Needs work <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "testuser"},
		},
		{
			ID:       3,
			State:    "APPROVED",
			Body:     "Fixed! <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "testuser"},
		},
	}
	review, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if !found {
		t.Fatal("expected match when multiple reviews exist")
	}
	if review.ID != 3 {
		t.Fatalf("expected latest review ID 3, got %d", review.ID)
	}
}

func TestFindLatestMatchingReview_CaseInsensitiveReviewer(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "APPROVED",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "TestUser"},
		},
	}
	review, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if !found {
		t.Fatal("expected match with case-insensitive reviewer login")
	}
	if review.ID != 1 {
		t.Fatalf("expected review ID 1, got %d", review.ID)
	}
}

func TestFindLatestMatchingReview_WhitespaceHandling(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "APPROVED",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "  abc123  ",
			User:     ghUser{Login: "  testuser  "},
		},
	}
	review, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if !found {
		t.Fatal("expected match with whitespace handling")
	}
	if review.ID != 1 {
		t.Fatalf("expected review ID 1, got %d", review.ID)
	}
}

func TestFindLatestMatchingReview_DismissedStateCaseInsensitive(t *testing.T) {
	t.Parallel()

	reviews := []ghReview{
		{
			ID:       1,
			State:    "dismissed",
			Body:     "LGTM <!-- chum-round:1 -->",
			CommitID: "abc123",
			User:     ghUser{Login: "testuser"},
		},
	}
	_, found := findLatestMatchingReview(reviews, "testuser", "abc123", 1)
	if found {
		t.Fatal("expected no match when review is dismissed (case-insensitive)")
	}
}

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

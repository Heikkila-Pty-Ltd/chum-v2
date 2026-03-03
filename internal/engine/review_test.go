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

func TestParseRepoSlug_SSH(t *testing.T) {
	t.Parallel()

	slug, err := parseRepoSlug("git@github.com:owner/repo.git")
	if err != nil {
		t.Fatalf("parseRepoSlug SSH with .git: %v", err)
	}
	if slug != "owner/repo" {
		t.Fatalf("slug = %q, want owner/repo", slug)
	}
}

func TestParseRepoSlug_HTTPS(t *testing.T) {
	t.Parallel()

	slug, err := parseRepoSlug("https://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("parseRepoSlug HTTPS with .git: %v", err)
	}
	if slug != "owner/repo" {
		t.Fatalf("slug = %q, want owner/repo", slug)
	}
}

func TestParseRepoSlug_HTTPSNoGit(t *testing.T) {
	t.Parallel()

	slug, err := parseRepoSlug("https://github.com/owner/repo")
	if err != nil {
		t.Fatalf("parseRepoSlug HTTPS without .git: %v", err)
	}
	if slug != "owner/repo" {
		t.Fatalf("slug = %q, want owner/repo", slug)
	}
}

func TestParseRepoSlug_HTTP(t *testing.T) {
	t.Parallel()

	slug, err := parseRepoSlug("http://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("parseRepoSlug HTTP with .git: %v", err)
	}
	if slug != "owner/repo" {
		t.Fatalf("slug = %q, want owner/repo", slug)
	}
}

func TestParseRepoSlug_InvalidURL(t *testing.T) {
	t.Parallel()

	_, err := parseRepoSlug("invalid-url")
	if err == nil {
		t.Fatal("parseRepoSlug with invalid URL should return error")
	}
	expectedMsg := "unsupported remote origin URL: invalid-url"
	if err.Error() != expectedMsg {
		t.Fatalf("error = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestParseRepoSlug_NoSlash(t *testing.T) {
	t.Parallel()

	_, err := parseRepoSlug("https://github.com/onlyowner")
	if err == nil {
		t.Fatal("parseRepoSlug without slash should return error")
	}
}

func TestReviewStateToOutcome_Approved(t *testing.T) {
	t.Parallel()

	outcome := reviewStateToOutcome("APPROVED")
	if outcome != ReviewApproved {
		t.Fatalf("reviewStateToOutcome(APPROVED) = %q, want %q", outcome, ReviewApproved)
	}
}

func TestReviewStateToOutcome_ChangesRequested(t *testing.T) {
	t.Parallel()

	outcome := reviewStateToOutcome("CHANGES_REQUESTED")
	if outcome != ReviewChangesRequested {
		t.Fatalf("reviewStateToOutcome(CHANGES_REQUESTED) = %q, want %q", outcome, ReviewChangesRequested)
	}
}

func TestReviewStateToOutcome_Commented(t *testing.T) {
	t.Parallel()

	outcome := reviewStateToOutcome("COMMENTED")
	if outcome != ReviewNoActivity {
		t.Fatalf("reviewStateToOutcome(COMMENTED) = %q, want %q", outcome, ReviewNoActivity)
	}
}

func TestReviewStateToOutcome_EmptyString(t *testing.T) {
	t.Parallel()

	outcome := reviewStateToOutcome("")
	if outcome != ReviewNoActivity {
		t.Fatalf("reviewStateToOutcome(\"\") = %q, want %q", outcome, ReviewNoActivity)
	}
}

func TestReviewStateToOutcome_CaseInsensitive(t *testing.T) {
	t.Parallel()

	outcome := reviewStateToOutcome("approved")
	if outcome != ReviewApproved {
		t.Fatalf("reviewStateToOutcome(approved) = %q, want %q", outcome, ReviewApproved)
	}

	outcome = reviewStateToOutcome("changes_requested")
	if outcome != ReviewChangesRequested {
		t.Fatalf("reviewStateToOutcome(changes_requested) = %q, want %q", outcome, ReviewChangesRequested)
	}
}

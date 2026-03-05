package engine

import (
	"strings"
	"testing"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/config"
	"go.temporal.io/sdk/testsuite"
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

func TestParseReviewSignal_IgnoresCLIPreambleNoise(t *testing.T) {
	t.Parallel()

	out := "Loaded cached credentials.\nHook registry initialized with 0 hook entries\nREQUEST_CHANGES\nAdd tests."
	signal, body, invalid := parseReviewSignal(out)
	if invalid {
		t.Fatal("expected valid signal with CLI preamble noise")
	}
	if signal != "REQUEST_CHANGES" {
		t.Fatalf("signal = %q, want REQUEST_CHANGES", signal)
	}
	if body != "Add tests." {
		t.Fatalf("body = %q, want reviewer rationale", body)
	}
}

func TestParseReviewSignal_ParsesSignalPrefixLine(t *testing.T) {
	t.Parallel()

	signal, body, invalid := parseReviewSignal("Signal: APPROVE\nLooks good.")
	if invalid {
		t.Fatal("expected valid signal from prefixed line")
	}
	if signal != "APPROVE" {
		t.Fatalf("signal = %q, want APPROVE", signal)
	}
	if body != "Looks good." {
		t.Fatalf("body = %q, want reviewer rationale", body)
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

func TestResolveReviewer_UsesConfiguredCrossProvider(t *testing.T) {
	t.Parallel()

	a := &Activities{
		Config: &config.Config{
			Providers: map[string]config.Provider{
				"claude": {
					CLI:      "claude",
					Model:    "claude-sonnet",
					Reviewer: "gemini",
					Enabled:  true,
				},
				"gemini": {
					CLI:     "gemini",
					Model:   "gemini-2.5-flash",
					Enabled: true,
				},
			},
		},
	}

	agent, model, cross := a.resolveReviewer("claude")
	if agent != "gemini" {
		t.Fatalf("reviewer agent = %q, want gemini", agent)
	}
	if model != "gemini-2.5-flash" {
		t.Fatalf("reviewer model = %q, want gemini-2.5-flash", model)
	}
	if !cross {
		t.Fatal("expected cross-provider reviewer")
	}
}

func TestResolveReviewer_FallsBackToAnyEnabledCrossProvider(t *testing.T) {
	t.Parallel()

	a := &Activities{
		Config: &config.Config{
			Providers: map[string]config.Provider{
				"claude": {
					CLI:      "claude",
					Model:    "claude-sonnet",
					Reviewer: "gemini",
					Enabled:  true,
				},
				"gemini": {
					CLI:     "gemini",
					Model:   "gemini-2.5-flash",
					Enabled: false,
				},
				"codex": {
					CLI:     "codex",
					Model:   "gpt-5-codex",
					Enabled: true,
				},
			},
		},
	}

	agent, model, cross := a.resolveReviewer("claude")
	if agent != "codex" {
		t.Fatalf("reviewer agent = %q, want codex", agent)
	}
	if model != "gpt-5-codex" {
		t.Fatalf("reviewer model = %q, want gpt-5-codex", model)
	}
	if !cross {
		t.Fatal("expected cross-provider reviewer")
	}
}

func TestResolveReviewer_NoCrossProviderAvailable(t *testing.T) {
	t.Parallel()

	a := &Activities{
		Config: &config.Config{
			Providers: map[string]config.Provider{
				"claude": {
					CLI:      "claude",
					Model:    "claude-sonnet",
					Reviewer: "claude",
					Enabled:  true,
				},
			},
		},
	}

	agent, model, cross := a.resolveReviewer("claude")
	if agent != "claude" {
		t.Fatalf("reviewer agent = %q, want claude", agent)
	}
	if model != "claude-sonnet" {
		t.Fatalf("reviewer model = %q, want claude-sonnet", model)
	}
	if cross {
		t.Fatal("did not expect cross-provider reviewer")
	}
}

func TestBuildReviewPrompt_AdversarialRole(t *testing.T) {
	t.Parallel()

	prompt := buildReviewPrompt(123, 2, "diff --git a x")
	if !strings.Contains(prompt, "adversarial reviewer") {
		t.Fatalf("expected adversarial reviewer instruction, got: %q", prompt)
	}
	if !strings.Contains(prompt, "If confidence is not high, choose REQUEST_CHANGES.") {
		t.Fatalf("expected strict request-changes bias, got: %q", prompt)
	}
}

func TestRunReviewActivity_RequireCrossProviderReviewEnforced(t *testing.T) {
	t.Parallel()

	a := &Activities{
		Config: &config.Config{
			General: config.General{
				RequireCrossProviderReview: true,
			},
			Providers: map[string]config.Provider{
				"claude": {
					CLI:      "claude",
					Model:    "claude-sonnet",
					Reviewer: "claude",
					Enabled:  true,
				},
			},
		},
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.RunReviewActivity)

	_, err := env.ExecuteActivity(a.RunReviewActivity, t.TempDir(), 1, 1, "claude")
	if err == nil {
		t.Fatal("expected cross-provider enforcement error, got nil")
	}
	if !strings.Contains(err.Error(), "cross-provider reviewer required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunReviewActivity_RequireCrossProviderReviewRejectsDisabledReviewer(t *testing.T) {
	t.Parallel()

	a := &Activities{
		Config: &config.Config{
			General: config.General{
				RequireCrossProviderReview: true,
			},
			Providers: map[string]config.Provider{
				"claude": {
					CLI:      "claude",
					Model:    "claude-sonnet",
					Reviewer: "gemini",
					Enabled:  true,
				},
				"gemini": {
					CLI:     "gemini",
					Model:   "gemini-2.5-flash",
					Enabled: false,
				},
			},
		},
	}

	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a.RunReviewActivity)

	_, err := env.ExecuteActivity(a.RunReviewActivity, t.TempDir(), 1, 1, "claude")
	if err == nil {
		t.Fatal("expected strict cross-provider enforcement error, got nil")
	}
	if !strings.Contains(err.Error(), "no enabled cross-provider reviewer") {
		t.Fatalf("unexpected error: %v", err)
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

func TestReviewToOutcome_CommentedFallbackMarker(t *testing.T) {
	t.Parallel()

	outcome := reviewToOutcome(ghReview{
		State: "COMMENTED",
		Body:  "fallback\n" + selfReviewRequestChangesFallbackMarker,
	})
	if outcome != ReviewChangesRequested {
		t.Fatalf("reviewToOutcome(commented fallback) = %q, want %q", outcome, ReviewChangesRequested)
	}
}

func TestReviewToOutcome_CommentedApproveFallbackMarker(t *testing.T) {
	t.Parallel()

	outcome := reviewToOutcome(ghReview{
		State: "COMMENTED",
		Body:  "fallback\n" + selfReviewApproveFallbackMarker,
	})
	if outcome != ReviewApproved {
		t.Fatalf("reviewToOutcome(commented approve fallback) = %q, want %q", outcome, ReviewApproved)
	}
}

func TestIsSelfRequestChangesError(t *testing.T) {
	t.Parallel()

	errMsg := "gh api repos/o/r/pulls/1/reviews -X POST --raw-field event=REQUEST_CHANGES: gh: Review Can not request changes on your own pull request (HTTP 422): exit status 1"
	if !isSelfRequestChangesError(assertErr(errMsg)) {
		t.Fatal("expected true for self request-changes 422")
	}
	if isSelfRequestChangesError(assertErr("gh: some other HTTP 422")) {
		t.Fatal("expected false for unrelated error")
	}
}

func TestIsSelfApproveError(t *testing.T) {
	t.Parallel()

	errMsg := "gh api repos/o/r/pulls/1/reviews -X POST --raw-field event=APPROVE: gh: Cannot approve your own pull request (HTTP 422): exit status 1"
	if !isSelfApproveError(assertErr(errMsg)) {
		t.Fatal("expected true for self approve 422")
	}
	if isSelfApproveError(assertErr("gh: some other HTTP 422")) {
		t.Fatal("expected false for unrelated error")
	}
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

func assertErr(msg string) error {
	return staticErr(msg)
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

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.temporal.io/sdk/activity"

	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/llm"
	"github.com/Heikkila-Pty-Ltd/chum-v2/internal/types"
)

type ghUser struct {
	Login string `json:"login"`
}

type ghReview struct {
	ID       int64  `json:"id"`
	State    string `json:"state"`
	Body     string `json:"body"`
	HTMLURL  string `json:"html_url"`
	CommitID string `json:"commit_id"`
	User     ghUser `json:"user"`
}

type ghReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

type ghPRMergeState struct {
	MergeStateStatus string `json:"mergeStateStatus"`
}

type ghPRState struct {
	State string `json:"state"`
}

const (
	selfReviewRequestChangesFallbackMarker = "<!-- chum-self-review-fallback:request_changes -->"
	selfReviewApproveFallbackMarker        = "<!-- chum-self-review-fallback:approve -->"
)

// RunReviewActivity runs the reviewer model in print mode and parses signal/body.
func (a *Activities) RunReviewActivity(ctx context.Context, workDir string, prNumber, round int, execAgent string) (*ReviewDraft, error) {
	logger := activity.GetLogger(ctx)

	reviewerAgent, reviewerModel, crossProvider, reviewerEnabled, stage := a.resolveReviewerWithStage(execAgent)
	logger.Debug("Reviewer resolved",
		"executor", llm.NormalizeCLIName(execAgent),
		"reviewer", llm.NormalizeCLIName(reviewerAgent),
		"model", reviewerModel,
		"cross_provider", crossProvider,
		"reviewer_enabled", reviewerEnabled,
		"stage", stage,
	)

	if a.Config != nil && a.Config.General.RequireCrossProviderReview && (!crossProvider || !reviewerEnabled) {
		return nil, fmt.Errorf("cross-provider reviewer required but no enabled cross-provider reviewer is available for executor %q", execAgent)
	}
	reviewerCLI := llm.NormalizeCLIName(reviewerAgent)
	if _, err := exec.LookPath(reviewerCLI); err != nil {
		return nil, fmt.Errorf("PREFLIGHT: reviewer CLI %q not found on PATH", reviewerCLI)
	}

	diffText, err := buildReviewDiffInput(ctx, workDir, prNumber)
	if err != nil {
		return nil, fmt.Errorf("build review diff input: %w", err)
	}

	prompt := buildReviewPrompt(prNumber, round, diffText)
	result, err := a.LLM.Plan(ctx, reviewerAgent, reviewerModel, workDir, prompt)
	if err != nil {
		return nil, fmt.Errorf("review CLI: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("review CLI exited %d: %s", result.ExitCode, types.Truncate(result.Output, 500))
	}

	signal, body, invalid := parseReviewSignal(result.Output)
	if invalid {
		logger.Warn("Reviewer produced invalid signal; coercing to REQUEST_CHANGES",
			"reviewer", reviewerAgent, "round", round, "cross_provider", crossProvider)
	}

	return &ReviewDraft{
		Signal:        signal,
		Body:          body,
		ReviewerAgent: reviewerAgent,
		ReviewerModel: reviewerModel,
	}, nil
}

// SubmitReviewActivity posts APPROVE/REQUEST_CHANGES review to GitHub.
// Idempotent for the same reviewer+head SHA+round marker.
func (a *Activities) SubmitReviewActivity(ctx context.Context, workDir string, prNumber, round int, reviewerLogin, headSHA, signal, body string) (*ReviewResult, error) {
	reviews, err := listPRReviews(ctx, workDir, prNumber)
	if err != nil {
		return nil, fmt.Errorf("list reviews before submit: %w", err)
	}
	if existing, ok := findLatestMatchingReview(reviews, reviewerLogin, headSHA, round); ok {
		return &ReviewResult{
			Outcome:   reviewStateToOutcome(existing.State),
			Reason:    "existing round-tagged review found",
			ReviewURL: existing.HTMLURL,
			Comments:  existing.Body,
			ReviewID:  existing.ID,
		}, nil
	}

	event := "REQUEST_CHANGES"
	if strings.ToUpper(strings.TrimSpace(signal)) == "APPROVE" {
		event = "APPROVE"
	}
	reviewBody := strings.TrimSpace(body)
	if reviewBody == "" {
		reviewBody = "Automated review."
	}
	reviewBody += "\n\n" + roundMarker(round)

	repoSlug, err := repoSlugFromWorkDir(ctx, workDir)
	if err != nil {
		return nil, err
	}

	out, err := submitPRReview(ctx, workDir, repoSlug, prNumber, event, reviewBody)
	if err != nil && event == "REQUEST_CHANGES" && isSelfRequestChangesError(err) {
		fallbackBody := reviewBody + "\n\n" + selfReviewRequestChangesFallbackMarker
		out, err = submitPRReview(ctx, workDir, repoSlug, prNumber, "COMMENT", fallbackBody)
	}
	if err != nil && event == "APPROVE" && isSelfApproveError(err) {
		fallbackBody := reviewBody + "\n\n" + selfReviewApproveFallbackMarker
		out, err = submitPRReview(ctx, workDir, repoSlug, prNumber, "COMMENT", fallbackBody)
	}
	if err != nil {
		return nil, fmt.Errorf("submit review: %w", err)
	}

	var posted ghReview
	if err := json.Unmarshal([]byte(out), &posted); err != nil {
		return nil, fmt.Errorf("parse submitted review JSON: %w", err)
	}

	return &ReviewResult{
		Outcome:   reviewToOutcome(posted),
		Reason:    "review submitted",
		ReviewURL: posted.HTMLURL,
		Comments:  posted.Body,
		ReviewID:  posted.ID,
	}, nil
}

// CheckPRStateActivity reads review state from GitHub scoped by reviewer+SHA+round.
func (a *Activities) CheckPRStateActivity(ctx context.Context, workDir string, prNumber, round int, reviewerLogin, headSHA string) (*ReviewResult, error) {
	reviews, err := listPRReviews(ctx, workDir, prNumber)
	if err != nil {
		return &ReviewResult{
			Outcome: ReviewerFailed,
			Reason:  err.Error(),
		}, nil
	}
	match, ok := findLatestMatchingReview(reviews, reviewerLogin, headSHA, round)
	if ok {
		return &ReviewResult{
			Outcome:   reviewToOutcome(match),
			Reason:    "matched review state",
			ReviewURL: match.HTMLURL,
			Comments:  match.Body,
			ReviewID:  match.ID,
		}, nil
	}

	return &ReviewResult{
		Outcome: ReviewNoActivity,
		Reason:  "no matching review for reviewer/head SHA/round",
	}, nil
}

// ReadReviewFeedbackActivity returns inline review thread comments for a review.
func (a *Activities) ReadReviewFeedbackActivity(ctx context.Context, workDir string, prNumber int, reviewID int64) (string, error) {
	if reviewID == 0 {
		return "", nil
	}
	repoSlug, err := repoSlugFromWorkDir(ctx, workDir)
	if err != nil {
		return "", err
	}

	out, err := runCommand(ctx, workDir, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews/%d/comments", repoSlug, prNumber, reviewID))
	if err != nil {
		return "", fmt.Errorf("fetch review comments: %w", err)
	}

	var comments []ghReviewComment
	if err := json.Unmarshal([]byte(out), &comments); err != nil {
		return "", fmt.Errorf("parse review comments JSON: %w", err)
	}
	if len(comments) == 0 {
		return "", nil
	}

	lines := make([]string, 0, len(comments))
	for _, c := range comments {
		pathLoc := c.Path
		if c.Line > 0 {
			pathLoc = fmt.Sprintf("%s:%d", c.Path, c.Line)
		}
		body := strings.TrimSpace(c.Body)
		if body == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s %s", pathLoc, body))
	}
	return types.Truncate(strings.Join(lines, "\n"), 4000), nil
}

// MergePRActivity checks mergeability then merges if clean.
func (a *Activities) MergePRActivity(ctx context.Context, workDir string, prNumber int) (*MergeResult, error) {
	state, raw, err := readMergeState(ctx, workDir, prNumber)
	if err != nil {
		return nil, err
	}

	attemptMerge := func() *MergeResult {
		out, err := runCommand(ctx, workDir, "gh", "pr", "merge", strconv.Itoa(prNumber), "--squash", "--delete-branch")
		if err == nil {
			return &MergeResult{Merged: true, Reason: out}
		}

		// When branch protection blocks the merge (e.g., required reviewers,
		// status checks), report merge_blocked and let a human resolve it.
		// Never escalate to --admin — that bypasses all branch protection rules
		// and combined with self-review could merge unreviewed code.
		if isBaseBranchPolicyBlocked(err) {
			return &MergeResult{Merged: false, SubReason: "merge_blocked", Reason: err.Error()}
		}
		return &MergeResult{Merged: false, SubReason: "merge_failed", Reason: err.Error()}
	}

	switch state {
	case "CLEAN":
		return attemptMerge(), nil
	case "BEHIND":
		if err := updatePRBranch(ctx, workDir, prNumber); err != nil {
			return &MergeResult{Merged: false, SubReason: "merge_blocked", Reason: "update-branch failed: " + err.Error()}, nil
		}
		refreshedState, refreshedRaw, err := readMergeState(ctx, workDir, prNumber)
		if err != nil {
			return &MergeResult{Merged: false, SubReason: "merge_blocked", Reason: "read merge state after update-branch: " + err.Error()}, nil
		}
		state, raw = refreshedState, refreshedRaw
		if state == "BLOCKED" && checksPending(raw) {
			return &MergeResult{Merged: false, SubReason: "checks_pending_timeout", Reason: "required checks still pending"}, nil
		}
		if state == "CLEAN" || state == "BLOCKED" {
			return attemptMerge(), nil
		}
		return &MergeResult{Merged: false, SubReason: "merge_blocked", Reason: "merge state " + state + " after update-branch"}, nil
	case "BLOCKED":
		if checksPending(raw) {
			return &MergeResult{Merged: false, SubReason: "checks_pending_timeout", Reason: "required checks still pending"}, nil
		}
		return attemptMerge(), nil
	case "DIRTY", "DRAFT", "UNKNOWN", "UNSTABLE", "HAS_HOOKS":
		return &MergeResult{Merged: false, SubReason: "merge_blocked", Reason: "merge state " + state}, nil
	default:
		return &MergeResult{Merged: false, SubReason: "merge_blocked", Reason: "merge state " + state}, nil
	}
}

// GuardReviewerCleanActivity ensures reviewer stage did not alter the worktree.
func (a *Activities) GuardReviewerCleanActivity(ctx context.Context, workDir string) error {
	out, err := runCommand(ctx, workDir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status --porcelain: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("reviewer modified worktree")
	}
	return nil
}

// ResolveReviewerLoginActivity resolves the GitHub login used by gh auth.
func (a *Activities) ResolveReviewerLoginActivity(ctx context.Context, workDir string) (string, error) {
	out, err := runCommand(ctx, workDir, "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("resolve reviewer login: %w", err)
	}
	login := strings.TrimSpace(out)
	if login == "" {
		return "", fmt.Errorf("empty reviewer login")
	}
	return login, nil
}

// DefaultReviewer maps execution provider to a cross-review provider.
func DefaultReviewer(agent string) string {
	switch llm.NormalizeCLIName(agent) {
	case "claude":
		return "gemini"
	case "gemini":
		return "claude"
	case "codex":
		return "claude"
	default:
		return "claude"
	}
}

type namedProvider struct {
	Name     string
	CLI      string
	Model    string
	Reviewer string
	Enabled  bool
}

func (a *Activities) resolveReviewer(execAgent string) (reviewerAgent string, reviewerModel string, crossProvider bool) {
	reviewerAgent, reviewerModel, crossProvider, _, _ = a.resolveReviewerWithStage(execAgent)
	return reviewerAgent, reviewerModel, crossProvider
}

func (a *Activities) resolveReviewerWithStage(execAgent string) (reviewerAgent string, reviewerModel string, crossProvider bool, reviewerEnabled bool, stage string) {
	execCLI := llm.NormalizeCLIName(execAgent)
	fallbackAgent := DefaultReviewer(execCLI)
	if a.Config == nil || len(a.Config.Providers) == 0 {
		return fallbackAgent, "", llm.NormalizeCLIName(fallbackAgent) != execCLI, false, "no_config_default_reviewer"
	}

	providers := make([]namedProvider, 0, len(a.Config.Providers))
	for name, p := range a.Config.Providers {
		providers = append(providers, namedProvider{
			Name:     name,
			CLI:      p.CLI,
			Model:    p.Model,
			Reviewer: p.Reviewer,
			Enabled:  p.Enabled,
		})
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Name < providers[j].Name
	})

	// 1) Executor-specific reviewer mapping, preferring enabled cross-provider reviewers.
	for _, p := range providers {
		if llm.NormalizeCLIName(p.CLI) != execCLI {
			continue
		}
		candidate, ok := findProviderByTarget(providers, p.Reviewer, true)
		if !ok {
			continue
		}
		cross := llm.NormalizeCLIName(candidate.CLI) != execCLI
		if cross {
			return candidate.CLI, candidate.Model, true, candidate.Enabled, "exec_override_enabled_cross"
		}
	}

	// 2) Default mapping among enabled providers.
	if candidate, ok := findProviderByTarget(providers, fallbackAgent, true); ok {
		cross := llm.NormalizeCLIName(candidate.CLI) != execCLI
		if cross {
			return candidate.CLI, candidate.Model, true, candidate.Enabled, "default_mapping_enabled_cross"
		}
	}

	// 3) Any enabled cross provider.
	if candidate, ok := firstCrossProvider(providers, execCLI, true); ok {
		return candidate.CLI, candidate.Model, true, candidate.Enabled, "any_enabled_cross"
	}

	// 4) Relax enabled requirement for explicitly configured reviewer.
	for _, p := range providers {
		if llm.NormalizeCLIName(p.CLI) != execCLI {
			continue
		}
		candidate, ok := findProviderByTarget(providers, p.Reviewer, false)
		if !ok {
			continue
		}
		cross := llm.NormalizeCLIName(candidate.CLI) != execCLI
		return candidate.CLI, candidate.Model, cross, candidate.Enabled, "exec_override_any"
	}

	// 5) Relax enabled requirement for default mapping.
	if candidate, ok := findProviderByTarget(providers, fallbackAgent, false); ok {
		cross := llm.NormalizeCLIName(candidate.CLI) != execCLI
		return candidate.CLI, candidate.Model, cross, candidate.Enabled, "default_mapping_any"
	}

	// 6) Last resort: any configured cross provider, then executor itself.
	if candidate, ok := firstCrossProvider(providers, execCLI, false); ok {
		return candidate.CLI, candidate.Model, true, candidate.Enabled, "any_configured_cross"
	}
	if candidate, ok := findProviderByTarget(providers, execCLI, false); ok {
		return candidate.CLI, candidate.Model, false, candidate.Enabled, "executor_self_fallback"
	}
	return fallbackAgent, "", llm.NormalizeCLIName(fallbackAgent) != execCLI, false, "default_reviewer_fallback"
}

func findProviderByTarget(providers []namedProvider, target string, onlyEnabled bool) (namedProvider, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return namedProvider{}, false
	}

	// Name match is intentionally preferred over CLI match so explicit provider-key
	// references (the map key in [providers.*]) are deterministic.
	for _, p := range providers {
		if !strings.EqualFold(p.Name, target) {
			continue
		}
		if onlyEnabled && !p.Enabled {
			continue
		}
		return p, true
	}

	targetCLI := llm.NormalizeCLIName(target)
	for _, p := range providers {
		if llm.NormalizeCLIName(p.CLI) != targetCLI {
			continue
		}
		if onlyEnabled && !p.Enabled {
			continue
		}
		return p, true
	}

	return namedProvider{}, false
}

func firstCrossProvider(providers []namedProvider, execCLI string, onlyEnabled bool) (namedProvider, bool) {
	for _, p := range providers {
		if onlyEnabled && !p.Enabled {
			continue
		}
		if llm.NormalizeCLIName(p.CLI) == execCLI {
			continue
		}
		return p, true
	}
	return namedProvider{}, false
}

func buildReviewPrompt(prNumber, round int, diff string) string {
	return fmt.Sprintf(`You are the adversarial reviewer for pull request #%d.
Assume defects exist until proven otherwise.
Prioritize correctness, regressions, security risks, data-loss paths, edge cases, and missing tests.
If confidence is not high, choose REQUEST_CHANGES.

Output contract (strict):
Line 1 must be exactly one of:
APPROVE
REQUEST_CHANGES

All following lines must be concise review rationale.
When requesting changes, list concrete findings with severity and, when possible, file/line pointers.
Do not include markdown code fences.

Review round: %d

PR diff:
%s
`, prNumber, round, diff)
}

func parseReviewSignal(output string) (signal string, body string, invalid bool) {
	output = llm.UnwrapClaudeJSON(output)
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")

	// Only check the first 3 non-blank lines for the signal. This prevents
	// matching "APPROVE" or "REQUEST_CHANGES" from echoed prompt instructions
	// deeper in the output (e.g., when the reviewer LLM echoes the prompt
	// before giving its actual verdict). The review prompt says "Line 1 must
	// be exactly one of: APPROVE / REQUEST_CHANGES", so the signal should
	// appear at the top.
	nonBlankSeen := 0
	const maxSignalLines = 3
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonBlankSeen++
		if nonBlankSeen > maxSignalLines {
			break
		}
		signal, inlineBody, ok := parseReviewSignalLine(line)
		if !ok {
			continue
		}
		rest := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		if inlineBody != "" {
			if rest != "" {
				rest = inlineBody + "\n" + rest
			} else {
				rest = inlineBody
			}
		}
		if rest == "" {
			if signal == "APPROVE" {
				rest = "Approved."
			} else {
				rest = "Changes requested."
			}
		}
		return signal, rest, false
	}

	if strings.TrimSpace(output) == "" {
		return "REQUEST_CHANGES", "Invalid reviewer output: empty response.", true
	}
	raw := types.Truncate(strings.TrimSpace(output), 2000)
	return "REQUEST_CHANGES", "Invalid reviewer signal. Expected APPROVE or REQUEST_CHANGES.\n\nRaw output:\n" + raw, true
}

func parseReviewSignalLine(line string) (signal string, inlineBody string, ok bool) {
	normalized := strings.TrimSpace(line)
	if normalized == "" {
		return "", "", false
	}
	normalized = strings.TrimSpace(strings.Trim(normalized, "`*#>_- "))

	for _, prefix := range []string{"SIGNAL:", "DECISION:", "VERDICT:"} {
		upperNormalized := strings.ToUpper(normalized)
		if strings.HasPrefix(upperNormalized, prefix) {
			normalized = strings.TrimSpace(normalized[len(prefix):])
			break
		}
	}
	canonical := strings.ToUpper(strings.TrimSpace(strings.Trim(normalized, "`*#>_- ")))

	if canonical == "APPROVE" || canonical == "REQUEST_CHANGES" {
		return canonical, "", true
	}

	upperNormalized := strings.ToUpper(normalized)
	for _, sig := range []string{"APPROVE", "REQUEST_CHANGES"} {
		if strings.HasPrefix(upperNormalized, sig+":") {
			body := strings.TrimSpace(normalized[len(sig)+1:])
			return sig, body, true
		}
		if strings.HasPrefix(upperNormalized, sig+" -") {
			body := strings.TrimSpace(normalized[len(sig)+2:])
			return sig, body, true
		}
	}
	return "", "", false
}

func buildReviewDiffInput(ctx context.Context, workDir string, prNumber int) (string, error) {
	out, err := runCommand(ctx, workDir, "gh", "pr", "diff", strconv.Itoa(prNumber))
	if err == nil && strings.TrimSpace(out) != "" {
		return capDiff(out), nil
	}

	out, err = runCommand(ctx, workDir, "git", "diff", "--no-ext-diff", "--minimal", "HEAD~1..HEAD")
	if err != nil {
		return "", fmt.Errorf("diff unavailable: %w", err)
	}
	return capDiff(out), nil
}

func capDiff(diff string) string {
	const maxBytes = 120000
	if len(diff) <= maxBytes {
		return diff
	}
	return diff[:maxBytes] + "\n\n[truncated by CHUM]"
}

func roundMarker(round int) string {
	return fmt.Sprintf("<!-- chum-round:%d -->", round)
}

func reviewStateToOutcome(state string) ReviewOutcome {
	return reviewStateWithBodyToOutcome(state, "")
}

func reviewToOutcome(review ghReview) ReviewOutcome {
	return reviewStateWithBodyToOutcome(review.State, review.Body)
}

func reviewStateWithBodyToOutcome(state, body string) ReviewOutcome {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "APPROVED":
		return ReviewApproved
	case "CHANGES_REQUESTED":
		return ReviewChangesRequested
	case "COMMENTED":
		if strings.Contains(body, selfReviewApproveFallbackMarker) {
			return ReviewApproved
		}
		if strings.Contains(body, selfReviewRequestChangesFallbackMarker) {
			return ReviewChangesRequested
		}
		return ReviewNoActivity
	default:
		return ReviewNoActivity
	}
}

func isSelfRequestChangesError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot request changes on your own pull request") ||
		strings.Contains(msg, "can not request changes on your own pull request")
}

func isSelfApproveError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot approve your own pull request") ||
		strings.Contains(msg, "can not approve your own pull request")
}

func submitPRReview(ctx context.Context, workDir, repoSlug string, prNumber int, event, reviewBody string) (string, error) {
	args := []string{
		"api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repoSlug, prNumber),
		"-X", "POST",
		"--raw-field", "event=" + event,
		"--raw-field", "body=" + reviewBody,
	}
	return runCommand(ctx, workDir, "gh", args...)
}

func listPRReviews(ctx context.Context, workDir string, prNumber int) ([]ghReview, error) {
	repoSlug, err := repoSlugFromWorkDir(ctx, workDir)
	if err != nil {
		return nil, fmt.Errorf("list PR reviews: %w", err)
	}

	out, err := runCommand(ctx, workDir, "gh", "api", fmt.Sprintf("repos/%s/pulls/%d/reviews", repoSlug, prNumber))
	if err != nil {
		return nil, fmt.Errorf("gh api reviews: %w", err)
	}
	var reviews []ghReview
	if err := json.Unmarshal([]byte(out), &reviews); err != nil {
		return nil, fmt.Errorf("parse reviews JSON: %w", err)
	}
	return reviews, nil
}

func findLatestMatchingReview(reviews []ghReview, reviewerLogin, headSHA string, round int) (ghReview, bool) {
	marker := roundMarker(round)
	reviewerLogin = strings.ToLower(strings.TrimSpace(reviewerLogin))
	headSHA = strings.TrimSpace(headSHA)

	var last ghReview
	found := false
	for _, r := range reviews {
		if strings.ToLower(strings.TrimSpace(r.User.Login)) != reviewerLogin {
			continue
		}
		if strings.TrimSpace(r.CommitID) != headSHA {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(r.State), "DISMISSED") {
			continue
		}
		if !strings.Contains(r.Body, marker) {
			continue
		}
		last = r
		found = true
	}
	return last, found
}

// repoSlugCache caches repo slugs per workDir to avoid repeated
// `git remote get-url origin` subprocesses (called 3-4 times per review round).
var repoSlugCache sync.Map

func repoSlugFromWorkDir(ctx context.Context, workDir string) (string, error) {
	if cached, ok := repoSlugCache.Load(workDir); ok {
		return cached.(string), nil
	}
	out, err := runCommand(ctx, workDir, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("resolve origin URL: %w", err)
	}
	slug, err := parseRepoSlug(strings.TrimSpace(out))
	if err != nil {
		return "", err
	}
	repoSlugCache.Store(workDir, slug)
	return slug, nil
}

func parseRepoSlug(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		s := strings.TrimPrefix(remote, "git@github.com:")
		s = strings.TrimSuffix(s, ".git")
		if strings.Contains(s, "/") {
			return s, nil
		}
	case strings.HasPrefix(remote, "https://github.com/"):
		s := strings.TrimPrefix(remote, "https://github.com/")
		s = strings.TrimSuffix(s, ".git")
		if strings.Contains(s, "/") {
			return s, nil
		}
	case strings.HasPrefix(remote, "http://github.com/"):
		s := strings.TrimPrefix(remote, "http://github.com/")
		s = strings.TrimSuffix(s, ".git")
		if strings.Contains(s, "/") {
			return s, nil
		}
	}
	return "", fmt.Errorf("unsupported remote origin URL: %s", remote)
}

func runCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

func readMergeState(ctx context.Context, workDir string, prNumber int) (state string, raw string, err error) {
	out, err := runCommand(ctx, workDir, "gh", "pr", "view", strconv.Itoa(prNumber), "--json", "mergeStateStatus,statusCheckRollup")
	if err != nil {
		return "", "", err
	}
	var payload ghPRMergeState
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return "", "", fmt.Errorf("parse merge state JSON: %w", err)
	}
	return strings.ToUpper(strings.TrimSpace(payload.MergeStateStatus)), out, nil
}

func checksPending(raw string) bool {
	return strings.Contains(raw, `"PENDING"`) || strings.Contains(raw, `"IN_PROGRESS"`) || strings.Contains(raw, `"QUEUED"`)
}

func updatePRBranch(ctx context.Context, workDir string, prNumber int) error {
	repoSlug, err := repoSlugFromWorkDir(ctx, workDir)
	if err != nil {
		return fmt.Errorf("resolve repo slug: %w", err)
	}
	_, err = runCommand(ctx, workDir, "gh", "api", "-X", "PUT", fmt.Sprintf("repos/%s/pulls/%d/update-branch", repoSlug, prNumber))
	if err != nil && isAlreadyUpToDateUpdateBranchError(err) {
		return nil
	}
	return err
}

func isAlreadyUpToDateUpdateBranchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not behind the base branch") ||
		strings.Contains(msg, "head branch is up to date")
}

func isBaseBranchPolicyBlocked(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "base branch policy prohibits the merge")
}

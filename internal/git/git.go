// Package git provides worktree management, DoD checks, and push/PR helpers.
package git

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// validTaskID matches safe task IDs: alphanumeric, dots, dashes, underscores.
// No path separators, no shell metacharacters, no spaces.
var validTaskID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// ValidateTaskID rejects task IDs that could cause path traversal or
// git branch name issues when used in branch names (chum/{taskID})
// and filesystem paths (/tmp/chum-worktrees/{taskID}).
func ValidateTaskID(taskID string) error {
	if taskID == "" {
		return errors.New("empty task ID")
	}
	if !validTaskID.MatchString(taskID) {
		return fmt.Errorf("invalid task ID %q: must match [a-zA-Z0-9._-]+", taskID)
	}
	if strings.Contains(taskID, "..") {
		return fmt.Errorf("invalid task ID %q: contains path traversal", taskID)
	}
	return nil
}

// SetupWorktree creates an isolated git worktree for a task.
// Handles stale branches/worktrees from previous failed runs.
// Returns the absolute path to the worktree directory.
func SetupWorktree(ctx context.Context, baseDir, taskID string) (string, error) {
	return SetupWorktreeAtRef(ctx, baseDir, taskID, "")
}

// SetupWorktreeAtRef creates an isolated git worktree for a task branch.
// When startRef is empty it uses the latest origin default branch.
// When startRef is set (for example a PR head SHA), the new branch starts there.
func SetupWorktreeAtRef(ctx context.Context, baseDir, taskID, startRef string) (string, error) {
	if err := ValidateTaskID(taskID); err != nil {
		return "", err
	}
	branch := fmt.Sprintf("chum/%s", taskID)
	wtDir := filepath.Join(os.TempDir(), "chum-worktrees", taskID)

	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		return "", fmt.Errorf("mkdir worktree parent: %w", err)
	}

	// Clean up stale worktree if it exists from a previous failed run
	if _, err := os.Stat(wtDir); err == nil {
		cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtDir)
		cmd.Dir = baseDir
		_ = cmd.Run() // best-effort
		os.RemoveAll(wtDir)
	}

	// If the target branch is still checked out in another worktree, remove that
	// worktree first so branch deletion/recreation can succeed.
	_ = removeBranchWorktrees(ctx, baseDir, branch, wtDir)

	// Delete stale branch if it exists (leftover from previous run)
	if out, err := runGitWithConfigLockRetry(ctx, baseDir, "branch", "-D", branch); err != nil {
		msg := strings.ToLower(string(out))
		// Branch missing is fine; any other error is actionable.
		if !strings.Contains(msg, "not found") && !strings.Contains(msg, "not a valid branch name") {
			return "", fmt.Errorf("delete stale branch %s: %s: %w", branch, string(out), err)
		}
	}

	var cmd *exec.Cmd

	// Prune any stale worktree entries
	cmd = exec.CommandContext(ctx, "git", "worktree", "prune")
	cmd.Dir = baseDir
	_ = cmd.Run()

	start := strings.TrimSpace(startRef)
	if start == "" {
		// Always start from latest origin default branch to avoid stale branches.
		// Without this, agents branch from whatever HEAD the factory workspace
		// had when it was last pulled — leading to PRs that accidentally revert
		// commits made since then.
		baseBranch, err := resolveDefaultBranch(ctx, baseDir)
		if err != nil {
			baseBranch = "master" // fallback; checkout below remains best-effort
		}
		cmd = exec.CommandContext(ctx, "git", "fetch", "origin", baseBranch)
		cmd.Dir = baseDir
		_ = cmd.Run() // best-effort; if offline we still branch from local HEAD

		candidate := "origin/" + baseBranch
		if gitRefExists(ctx, baseDir, candidate) {
			start = candidate
			cmd = exec.CommandContext(ctx, "git", "checkout", start)
			cmd.Dir = baseDir
			_ = cmd.Run() // best-effort; detached HEAD is fine for worktree base
		} else {
			start = "HEAD"
		}
	} else {
		// Ensure requested ref is available locally when possible.
		cmd = exec.CommandContext(ctx, "git", "fetch", "origin")
		cmd.Dir = baseDir
		_ = cmd.Run() // best-effort
	}

	// Create the worktree on a new branch.
	// Use -c core.hooksPath=/dev/null to bypass any project hooks (e.g. beads)
	// that may reference tools not installed in the execution environment.
	if out, err := runGitWithConfigLockRetry(ctx, baseDir, "-c", "core.hooksPath=/dev/null",
		"worktree", "add", "-b", branch, wtDir, start); err != nil {
		return "", fmt.Errorf("git worktree add (start=%s): %s: %w", start, string(out), err)
	}

	// Configure the worktree: set author and disable hooks so the agent
	// CLI doesn't trigger beads/bd hooks during its own git operations.
	for _, kv := range [][2]string{
		{"user.name", "CHUM v2"},
		{"user.email", "chum@localhost"},
		{"core.hooksPath", "/dev/null"},
	} {
		cmd = exec.CommandContext(ctx, "git", "config", kv[0], kv[1])
		cmd.Dir = wtDir
		_ = cmd.Run() // best-effort
	}

	return wtDir, nil
}

func removeBranchWorktrees(ctx context.Context, baseDir, branch, keepPath string) error {
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = baseDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}

	branchRef := "refs/heads/" + branch
	scanner := bufio.NewScanner(strings.NewReader(string(out)))

	var (
		path string
		ref  string
	)
	flush := func() error {
		if path == "" {
			return nil
		}
		defer func() {
			path = ""
			ref = ""
		}()
		if ref != branchRef {
			return nil
		}
		if path == keepPath || path == baseDir {
			return nil
		}
		rm := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", path)
		rm.Dir = baseDir
		if rmOut, rmErr := rm.CombinedOutput(); rmErr != nil {
			return fmt.Errorf("remove conflicting worktree %s for %s: %s: %w", path, branch, string(rmOut), rmErr)
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			ref = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan worktree list: %w", err)
	}
	if err := flush(); err != nil {
		return err
	}
	return nil
}

// ErrOnProtectedBranch is returned when an operation is attempted on master/main.
var ErrOnProtectedBranch = errors.New("on protected branch")

// AssertFeatureBranch returns an error if workDir is on master or main.
// All agent operations must happen on feature branches in isolated worktrees.
func AssertFeatureBranch(ctx context.Context, workDir string) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("cannot determine branch: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "master" || branch == "main" {
		return fmt.Errorf("%w: refusing to operate on %s — agents must work on feature branches", ErrOnProtectedBranch, branch)
	}
	return nil
}

// CleanupWorktree removes a git worktree.
func CleanupWorktree(ctx context.Context, baseDir, wtDir string) error {
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtDir)
	cmd.Dir = baseDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", string(out), err)
	}
	return nil
}

// Push pushes the current branch to origin.
func Push(ctx context.Context, workDir string) error {
	cmd := exec.CommandContext(ctx, "git", "push", "-u", "origin", "HEAD")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		if isNonFastForwardPushError(err, out) {
			branch, branchErr := currentBranch(ctx, workDir)
			if branchErr == nil && strings.HasPrefix(branch, "chum/") {
				retry := exec.CommandContext(ctx, "git", "push", "--force-with-lease", "-u", "origin", "HEAD")
				retry.Dir = workDir
				if retryOut, retryErr := retry.CombinedOutput(); retryErr == nil {
					return nil
				} else {
					return fmt.Errorf("git push (force-with-lease fallback): %s: %w", string(retryOut), retryErr)
				}
			}
		}
		return fmt.Errorf("git push: %s: %w", string(out), err)
	}
	return nil
}

func currentBranch(ctx context.Context, workDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --abbrev-ref HEAD: %s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func isNonFastForwardPushError(err error, out []byte) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(string(out))
	return strings.Contains(msg, "non-fast-forward") ||
		strings.Contains(msg, "fetch first") ||
		strings.Contains(msg, "rejected")
}

func runGitWithConfigLockRetry(ctx context.Context, dir string, args ...string) ([]byte, error) {
	const maxAttempts = 5
	backoff := 120 * time.Millisecond
	var (
		out []byte
		err error
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err = cmd.CombinedOutput()
		if err == nil {
			return out, nil
		}
		if !isGitConfigLockError(out) || attempt == maxAttempts {
			return out, err
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
	return out, err
}

func isGitConfigLockError(out []byte) bool {
	msg := strings.ToLower(string(out))
	return strings.Contains(msg, "could not lock config file") ||
		strings.Contains(msg, ".git/config.lock")
}

// CommitAll stages all changes and creates a commit.
// Returns true if a commit was created, false if nothing to commit.
func CommitAll(ctx context.Context, workDir, message string) (bool, error) {
	// Stage everything
	cmd := exec.CommandContext(ctx, "git", "add", "-A")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git add: %s: %w", string(out), err)
	}

	// Check if there's anything staged
	cmd = exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = workDir
	if err := cmd.Run(); err == nil {
		return false, nil // nothing to commit
	}

	// Commit
	cmd = exec.CommandContext(ctx, "git", "commit", "-m", message)
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git commit: %s: %w", string(out), err)
	}
	return true, nil
}

// CreatePR creates a pull request using gh CLI.
// Falls back to logging the branch name if gh is not installed.
func CreatePR(ctx context.Context, workDir, title string) error {
	// Preflight: check gh exists
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not installed — push succeeded but PR must be created manually")
	}

	args := []string{"pr", "create",
		"--title", title,
		"--body", "Auto-generated by CHUM v2",
	}
	if baseBranch, err := resolveDefaultBranch(ctx, workDir); err == nil && baseBranch != "" {
		args = append(args, "--base", baseBranch)
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr create: %s: %w", string(out), err)
	}
	return nil
}

// CheckResult is the outcome of a single DoD check command.
type CheckResult struct {
	Command  string `json:"command"`
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// DoDResult is the aggregate outcome of all DoD checks.
type DoDResult struct {
	Passed   bool          `json:"passed"`
	Checks   []CheckResult `json:"checks"`
	Failures []string      `json:"failures"`
}

// PRInfo captures pull request metadata from gh.
type PRInfo struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"headRefOid"`
	URL     string `json:"url"`
}

// RunDoDChecks validates worktree integrity, checks for actual changes,
// then executes each check command. Returns the aggregate result.
func RunDoDChecks(ctx context.Context, workDir string, checks []string) DoDResult {
	result := DoDResult{Passed: true}

	// --- Preflight 1: worktree integrity ---
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return DoDResult{
			Passed: false,
			Failures: []string{fmt.Sprintf(
				"WORKTREE BROKEN: .git missing in %s — worktree was likely cleaned while workflow was running",
				workDir)},
		}
	}

	// --- Preflight 2: non-empty diff ---
	hasChanges, err := HasChanges(ctx, workDir)
	if err != nil {
		// Non-fatal — proceed to checks anyway
	} else if !hasChanges {
		return DoDResult{
			Passed:   false,
			Failures: []string{"NO CHANGES: agent produced no code changes — diff is empty"},
		}
	}

	// --- Preflight 3: npm check ---
	for _, check := range checks {
		if strings.Contains(check, "npm ") {
			if _, err := os.Stat(filepath.Join(workDir, "package.json")); err != nil {
				return DoDResult{
					Passed: false,
					Failures: []string{fmt.Sprintf(
						"WORKTREE BROKEN: package.json missing in %s (required for: %s)",
						workDir, check)},
				}
			}
			break
		}
	}

	// --- Run configured checks ---
	for _, check := range checks {
		cr := runCheck(ctx, workDir, check)
		result.Checks = append(result.Checks, cr)
		if !cr.Passed {
			result.Passed = false
			result.Failures = append(result.Failures,
				fmt.Sprintf("%s (exit %d)", check, cr.ExitCode))
		}
	}
	return result
}

// HasChanges returns true if the worktree has any diff vs its merge base.
// Checks both committed and uncommitted changes.
func HasChanges(ctx context.Context, workDir string) (bool, error) {
	// Check for uncommitted changes first
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return true, nil // uncommitted changes exist
	}

	// Check for committed changes vs origin/HEAD or main
	for _, base := range []string{"main", "master", "HEAD~1"} {
		cmd = exec.CommandContext(ctx, "git", "diff", "--stat", base)
		cmd.Dir = workDir
		out, err = cmd.Output()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false, err
			}
			continue // try next base
		}
		if len(strings.TrimSpace(string(out))) > 0 {
			return true, nil
		}
	}

	return false, nil
}

func runCheck(ctx context.Context, workDir, command string) CheckResult {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return CheckResult{Command: command, Passed: true}
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	cr := CheckResult{
		Command: command,
		Output:  string(out),
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			cr.ExitCode = exitErr.ExitCode()
		} else {
			cr.ExitCode = 1
		}
		cr.Passed = false
	} else {
		cr.Passed = true
	}
	return cr
}

// GetPRInfo returns PR number/head SHA/url for the current branch or a specific PR.
func GetPRInfo(ctx context.Context, workDir string, prNumber int) (*PRInfo, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not installed")
	}

	args := []string{"pr", "view", "--json", "number,headRefOid,url"}
	if prNumber > 0 {
		args = []string{"pr", "view", fmt.Sprintf("%d", prNumber), "--json", "number,headRefOid,url"}
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr view: %s: %w", string(out), err)
	}

	var info PRInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse gh pr view JSON: %w", err)
	}
	if info.Number == 0 {
		return nil, fmt.Errorf("gh pr view returned empty PR number")
	}
	return &info, nil
}

// CreatePRInfo creates a PR and then returns PR metadata for the current branch.
func CreatePRInfo(ctx context.Context, workDir, title string) (*PRInfo, error) {
	if err := CreatePR(ctx, workDir, title); err != nil {
		return nil, err
	}
	return GetPRInfo(ctx, workDir, 0)
}

func resolveDefaultBranch(ctx context.Context, workDir string) (string, error) {
	for _, args := range [][]string{
		{"symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"},
		{"rev-parse", "--abbrev-ref", "origin/HEAD"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		branch := strings.TrimSpace(string(out))
		branch = strings.TrimPrefix(branch, "origin/")
		if branch != "" && branch != "HEAD" {
			return branch, nil
		}
	}
	for _, branch := range []string{"main", "master"} {
		cmd := exec.CommandContext(ctx, "git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
		cmd.Dir = workDir
		if err := cmd.Run(); err == nil {
			return branch, nil
		}
	}
	return "", fmt.Errorf("could not determine default branch from origin")
}

func gitRefExists(ctx context.Context, workDir, ref string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = workDir
	return cmd.Run() == nil
}

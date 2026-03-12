package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
	return string(out)
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, "", "init", dir)
	runGit(t, dir, "checkout", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func setOriginHeadRef(t *testing.T, repo, branch string) {
	t.Helper()
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/"+branch, head)
	runGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+branch)
}

func TestRunDoDChecks_FailsWhenGitDirMissing(t *testing.T) {
	t.Parallel()

	res := RunDoDChecks(context.Background(), t.TempDir(), []string{"echo ok"})
	if res.Passed {
		t.Fatal("expected DoD to fail when .git is missing")
	}
	if len(res.Failures) == 0 || !strings.Contains(res.Failures[0], "WORKTREE BROKEN") {
		t.Fatalf("unexpected failures: %v", res.Failures)
	}
}

func TestRunDoDChecks_FailsWhenNoChanges(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	res := RunDoDChecks(context.Background(), repo, []string{"echo ok"})
	if res.Passed {
		t.Fatal("expected DoD to fail for empty diff")
	}
	if len(res.Failures) == 0 || !strings.Contains(res.Failures[0], "NO CHANGES") {
		t.Fatalf("unexpected failures: %v", res.Failures)
	}
}

func TestRunDoDChecks_FailsWhenNpmCheckAndPackageJSONMissing(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("mutate README.md: %v", err)
	}

	res := RunDoDChecks(context.Background(), repo, []string{"npm test"})
	if res.Passed {
		t.Fatal("expected DoD to fail when npm check is configured without package.json")
	}
	if len(res.Failures) == 0 || !strings.Contains(res.Failures[0], "package.json missing") {
		t.Fatalf("unexpected failures: %v", res.Failures)
	}
}

func TestRunDoDChecks_CollectsFailureExitCode(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("mutate README.md: %v", err)
	}

	res := RunDoDChecks(context.Background(), repo, []string{"false"})
	if res.Passed {
		t.Fatal("expected DoD to fail for false command")
	}
	if len(res.Checks) != 1 {
		t.Fatalf("expected 1 check result, got %d", len(res.Checks))
	}
	if res.Checks[0].ExitCode == 0 {
		t.Fatalf("expected non-zero exit code, got %d", res.Checks[0].ExitCode)
	}
	if len(res.Failures) == 0 || !strings.Contains(res.Failures[0], "false (exit") {
		t.Fatalf("unexpected failures: %v", res.Failures)
	}
}

func TestHasChanges(t *testing.T) {
	t.Parallel()

	t.Run("clean repo", func(t *testing.T) {
		repo := initRepo(t)
		got, err := HasChanges(context.Background(), repo)
		if err != nil {
			t.Fatalf("HasChanges error: %v", err)
		}
		if got {
			t.Fatal("expected no changes")
		}
	})

	t.Run("uncommitted diff", func(t *testing.T) {
		repo := initRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("mutate README.md: %v", err)
		}
		got, err := HasChanges(context.Background(), repo)
		if err != nil {
			t.Fatalf("HasChanges error: %v", err)
		}
		if !got {
			t.Fatal("expected changes to be detected")
		}
	})
}

func TestSetupWorktree_ConfiguresHooksBypass(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	wtDir, err := SetupWorktree(context.Background(), repo, taskID)
	if err != nil {
		t.Fatalf("SetupWorktree error: %v", err)
	}
	defer func() { _ = CleanupWorktree(context.Background(), repo, wtDir) }()

	cmd := exec.Command("git", "config", "--get", "core.hooksPath")
	cmd.Dir = wtDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git config core.hooksPath failed: %v (%s)", err, string(out))
	}
	if strings.TrimSpace(string(out)) != "/dev/null" {
		t.Fatalf("hooksPath = %q, want /dev/null", strings.TrimSpace(string(out)))
	}
}

func TestSetupWorktree_RemovesConflictingBranchWorktree(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	taskID := fmt.Sprintf("task-conflict-%d", time.Now().UnixNano())
	branch := fmt.Sprintf("chum/%s", taskID)
	conflictDir := filepath.Join(t.TempDir(), "conflict-wt")

	runGit(t, repo, "worktree", "add", "-b", branch, conflictDir, "HEAD")

	wtDir, err := SetupWorktree(context.Background(), repo, taskID)
	if err != nil {
		t.Fatalf("SetupWorktree error: %v", err)
	}
	defer func() { _ = CleanupWorktree(context.Background(), repo, wtDir) }()

	list := runGit(t, repo, "worktree", "list")
	if strings.Contains(list, conflictDir) {
		t.Fatalf("expected conflicting worktree %q to be removed, got list:\n%s", conflictDir, list)
	}
	if !strings.Contains(list, wtDir) {
		t.Fatalf("expected new worktree %q to exist, got list:\n%s", wtDir, list)
	}
}

func TestSetupWorktreeAtRef_StartsFromProvidedRef(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	startRef := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("new line\n"), 0o644); err != nil {
		t.Fatalf("mutate README.md: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "second commit")

	taskID := fmt.Sprintf("task-start-ref-%d", time.Now().UnixNano())
	wtDir, err := SetupWorktreeAtRef(context.Background(), repo, taskID, startRef)
	if err != nil {
		t.Fatalf("SetupWorktreeAtRef error: %v", err)
	}
	defer func() { _ = CleanupWorktree(context.Background(), repo, wtDir) }()

	head := strings.TrimSpace(runGit(t, wtDir, "rev-parse", "HEAD"))
	if head != startRef {
		t.Fatalf("worktree HEAD = %q, want %q", head, startRef)
	}
}

func TestResolveDefaultBranch_FromOriginHead(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	setOriginHeadRef(t, repo, "master")

	got, err := resolveDefaultBranch(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolveDefaultBranch error: %v", err)
	}
	if got != "master" {
		t.Fatalf("branch = %q, want master", got)
	}
}

func TestResolveDefaultBranch_FallbackToMainRef(t *testing.T) {
	t.Parallel()

	repo := initRepo(t)
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", head)

	got, err := resolveDefaultBranch(context.Background(), repo)
	if err != nil {
		t.Fatalf("resolveDefaultBranch error: %v", err)
	}
	if got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
}

func TestCreatePR_UsesResolvedBaseBranch(t *testing.T) {
	repo := initRepo(t)
	setOriginHeadRef(t, repo, "master")

	binDir := t.TempDir()
	argsFile := filepath.Join(binDir, "gh.args")
	ghPath := filepath.Join(binDir, "gh")
	script := `#!/bin/sh
: > "$GH_ARGS_FILE"
for a in "$@"; do
  printf '%s\n' "$a" >> "$GH_ARGS_FILE"
done
`
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}

	t.Setenv("GH_ARGS_FILE", argsFile)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := CreatePR(context.Background(), repo, "Test PR"); err != nil {
		t.Fatalf("CreatePR error: %v", err)
	}

	argsRaw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read gh args: %v", err)
	}
	args := string(argsRaw)
	if !strings.Contains(args, "--base\nmaster\n") {
		t.Fatalf("expected --base master in gh args, got:\n%s", args)
	}
}

func initRepoWithRemote(t *testing.T) (repo string, remote string) {
	t.Helper()
	remote = filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)

	repo = initRepo(t)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")
	return repo, remote
}

func TestPush_ForceWithLeaseFallbackOnChumBranch(t *testing.T) {
	t.Parallel()

	repo, _ := initRepoWithRemote(t)

	// Create remote branch tip A.
	runGit(t, repo, "checkout", "-b", "chum/task-push-fallback")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("remote tip A\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "remote tip A")
	runGit(t, repo, "push", "-u", "origin", "HEAD")

	// Recreate same local branch from main to force a non-fast-forward push.
	runGit(t, repo, "checkout", "main")
	runGit(t, repo, "fetch", "origin")
	runGit(t, repo, "branch", "-D", "chum/task-push-fallback")
	runGit(t, repo, "checkout", "-b", "chum/task-push-fallback")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("local tip B\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "local tip B")

	if err := Push(context.Background(), repo); err != nil {
		t.Fatalf("Push fallback failed: %v", err)
	}

	localHead := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(runGit(t, repo, "rev-parse", "origin/chum/task-push-fallback"))
	if localHead != remoteHead {
		t.Fatalf("remote head = %s, want %s", remoteHead, localHead)
	}
}

func TestPush_NonFastForwardNonChumBranchFails(t *testing.T) {
	t.Parallel()

	repo, _ := initRepoWithRemote(t)

	// Create remote branch tip A.
	runGit(t, repo, "checkout", "-b", "feature/task-no-force")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("remote tip A\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "remote tip A")
	runGit(t, repo, "push", "-u", "origin", "HEAD")

	// Recreate same local branch from main to force non-fast-forward.
	runGit(t, repo, "checkout", "main")
	runGit(t, repo, "fetch", "origin")
	runGit(t, repo, "branch", "-D", "feature/task-no-force")
	runGit(t, repo, "checkout", "-b", "feature/task-no-force")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("local tip B\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "local tip B")

	err := Push(context.Background(), repo)
	if err == nil {
		t.Fatal("expected non-fast-forward push on non-chum branch to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "non-fast-forward") {
		t.Fatalf("error = %v, want non-fast-forward hint", err)
	}
}

func TestValidateTaskID(t *testing.T) {
	t.Parallel()

	valid := []string{
		"chum-ues.1.2",
		"task-123",
		"my_task",
		"TASK.v2",
		"a",
	}
	for _, id := range valid {
		if err := ValidateTaskID(id); err != nil {
			t.Errorf("ValidateTaskID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{
		"",
		"../etc/passwd",
		"task/../../root",
		"task id with spaces",
		"task;rm -rf /",
		"task$(evil)",
		"task`cmd`",
		"task\ninjection",
	}
	for _, id := range invalid {
		if err := ValidateTaskID(id); err == nil {
			t.Errorf("ValidateTaskID(%q) = nil, want error", id)
		}
	}
}

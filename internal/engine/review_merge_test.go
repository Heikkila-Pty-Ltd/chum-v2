package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}

func initMergeTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitCmd(t, "", "init", repo)
	runGitCmd(t, repo, "config", "user.name", "Test User")
	runGitCmd(t, repo, "config", "user.email", "test@example.com")
	runGitCmd(t, repo, "remote", "add", "origin", "https://github.com/o/r.git")
	return repo
}

func TestMergePRActivity_BehindUpdatesBranchThenMerges(t *testing.T) {
	repo := initMergeTestRepo(t)
	binDir := t.TempDir()
	ghPath := filepath.Join(binDir, "gh")
	stateFile := filepath.Join(binDir, "state.json")
	if err := os.WriteFile(stateFile, []byte(`{"mergeStateStatus":"BEHIND","statusCheckRollup":[]}`), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	script := `#!/bin/sh
set -eu
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  cat "$GH_STATE_FILE"
  exit 0
fi
if [ "$1" = "api" ] && [ "$2" = "-X" ] && [ "$3" = "PUT" ]; then
  echo '{"mergeStateStatus":"CLEAN","statusCheckRollup":[]}' > "$GH_STATE_FILE"
  echo '{}'
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  echo 'merged'
  exit 0
fi
echo "unexpected gh invocation: $@" >&2
exit 1
`
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("GH_STATE_FILE", stateFile)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := &Activities{}
	res, err := a.MergePRActivity(context.Background(), repo, 123)
	if err != nil {
		t.Fatalf("MergePRActivity error: %v", err)
	}
	if !res.Merged {
		t.Fatalf("expected merged result, got %+v", res)
	}
}

func TestMergePRActivity_BlockedUsesAdminFallback(t *testing.T) {
	repo := initMergeTestRepo(t)
	binDir := t.TempDir()
	ghPath := filepath.Join(binDir, "gh")
	script := `#!/bin/sh
set -eu
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  echo '{"mergeStateStatus":"BLOCKED","statusCheckRollup":[{"state":"SUCCESS"}]}'
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  admin=0
  for a in "$@"; do
    if [ "$a" = "--admin" ]; then
      admin=1
    fi
  done
  if [ "$admin" = "1" ]; then
    echo 'merged-admin'
    exit 0
  fi
  echo 'X Pull request is not mergeable: the base branch policy prohibits the merge.' >&2
  exit 1
fi
echo "unexpected gh invocation: $@" >&2
exit 1
`
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := &Activities{}
	res, err := a.MergePRActivity(context.Background(), repo, 49)
	if err != nil {
		t.Fatalf("MergePRActivity error: %v", err)
	}
	if !res.Merged {
		t.Fatalf("expected merged result, got %+v", res)
	}
	if !strings.Contains(res.Reason, "merged-admin") {
		t.Fatalf("merge reason = %q, want admin fallback output", res.Reason)
	}
}

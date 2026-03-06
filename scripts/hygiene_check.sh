#!/usr/bin/env bash
set -euo pipefail

ci_mode=0
if [ "${1:-}" = "--ci" ]; then
  ci_mode=1
fi

branch=$(git branch --show-current 2>/dev/null || true)
repo_root=$(git rev-parse --show-toplevel)
git_dir=$(git rev-parse --git-dir)
git_common_dir=$(git rev-parse --git-common-dir)

die() {
  echo "hygiene_check: $*" >&2
  exit 1
}

if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
  die "refusing to work on $branch; create a topic branch in a linked worktree"
fi

if [ "$ci_mode" -eq 0 ]; then
  git_dir_abs=$(cd "$repo_root/$git_dir" 2>/dev/null && pwd || cd "$git_dir" && pwd)
  git_common_dir_abs=$(cd "$repo_root/$git_common_dir" 2>/dev/null && pwd || cd "$git_common_dir" && pwd)
  if [ "$git_dir_abs" = "$git_common_dir_abs" ]; then
    die "primary checkout detected at $repo_root; use bash scripts/new_worktree.sh <branch-name>"
  fi
fi

echo "hygiene_check: ok"

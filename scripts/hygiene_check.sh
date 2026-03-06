#!/usr/bin/env bash
set -euo pipefail

ci_mode=0
if [ "${1:-}" = "--ci" ]; then
  ci_mode=1
fi

repo_root=$(git rev-parse --show-toplevel)
git_dir=$(git rev-parse --git-dir)
git_common_dir=$(git rev-parse --git-common-dir)
branch=$(git branch --show-current 2>/dev/null || true)

abs_path() {
  local maybe_rel=$1
  if [ -d "$repo_root/$maybe_rel" ]; then
    (cd "$repo_root/$maybe_rel" && pwd)
  else
    (cd "$maybe_rel" && pwd)
  fi
}

die() {
  echo "hygiene_check: $*" >&2
  exit 1
}

if [ "$ci_mode" -eq 1 ]; then
  if [ -n "${GITHUB_HEAD_REF:-}" ]; then
    branch=${GITHUB_HEAD_REF}
  elif [ -n "${GITHUB_REF_NAME:-}" ]; then
    branch=${GITHUB_REF_NAME}
  fi
  if [ -z "$branch" ]; then
    die "unable to determine branch in CI mode"
  fi
else
  hooks_path=$(git config --get core.hooksPath || true)
  if [ "$hooks_path" != ".githooks" ]; then
    die "core.hooksPath is '$hooks_path' (expected '.githooks'); run bash scripts/install_hooks.sh"
  fi

  git_dir_abs=$(abs_path "$git_dir")
  git_common_dir_abs=$(abs_path "$git_common_dir")
  if [ "$git_dir_abs" = "$git_common_dir_abs" ]; then
    die "primary checkout detected at $repo_root; use bash scripts/new_worktree.sh <branch-name>"
  fi
fi

if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
  die "refusing to work on $branch; create a topic branch in a linked worktree"
fi

echo "hygiene_check: ok"

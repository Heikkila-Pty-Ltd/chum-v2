#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 <branch-name> [start-point]" >&2
  exit 1
fi

branch=$1
start_point=${2:-HEAD}
repo_root=$(git rev-parse --show-toplevel)
common_git_dir=$(git rev-parse --git-common-dir)
common_git_dir_abs=$(cd "$repo_root/$common_git_dir" 2>/dev/null && pwd || cd "$common_git_dir" && pwd)
primary_checkout=$(dirname "$common_git_dir_abs")
repo_name=$(basename "$primary_checkout")
repo_parent=$(dirname "$primary_checkout")
worktree_root="$repo_parent/_worktrees"
safe_branch=$(printf '%s' "$branch" | tr '/' '-')
worktree_path="$worktree_root/${repo_name}-${safe_branch}"

mkdir -p "$worktree_root"

if git show-ref --verify --quiet "refs/heads/$branch"; then
  git worktree add "$worktree_path" "$branch"
else
  git worktree add -b "$branch" "$worktree_path" "$start_point"
fi

echo "$worktree_path"

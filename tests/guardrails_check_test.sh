#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
guardrails_script="$repo_root/scripts/guardrails_check.sh"
tmp_root=$(mktemp -d)
trap 'rm -rf "$tmp_root"' EXIT

new_repo() {
  local dir
  dir=$(mktemp -d "$tmp_root/repo.XXXXXX")
  git init -q "$dir"
  printf '%s\n' "$dir"
}

expect_pass() {
  local dir=$1
  (cd "$dir" && bash "$guardrails_script")
}

expect_fail() {
  local dir=$1
  if (cd "$dir" && bash "$guardrails_script" >/dev/null 2>&1); then
    echo "guardrails_check_test: expected failure in $dir" >&2
    exit 1
  fi
}

clean_repo=$(new_repo)
cat >"$clean_repo/ok.go" <<'EOF'
package ok
EOF
(cd "$clean_repo" && git add ok.go)
expect_pass "$clean_repo"

todo_repo=$(new_repo)
printf 'package bad\n%s\nfunc x() {}\n' '// TODO: missing issue id' >"$todo_repo/bad.go"
(cd "$todo_repo" && git add bad.go)
expect_fail "$todo_repo"

tracked_todo_repo=$(new_repo)
printf 'package good\n%s\nfunc x() {}\n' '// TODO(bd-123): tracked follow-up' >"$tracked_todo_repo/good.go"
(cd "$tracked_todo_repo" && git add good.go)
expect_pass "$tracked_todo_repo"

conflict_repo=$(new_repo)
printf 'package conflict\n%s\nfunc left() {}\n%s\n' '<<<<<<< HEAD' '>>>>>>> other' >"$conflict_repo/conflict.go"
(cd "$conflict_repo" && git add conflict.go)
expect_fail "$conflict_repo"

echo "guardrails_check_test: ok"

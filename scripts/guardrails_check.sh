#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

die() {
  echo "guardrails_check: $*" >&2
  exit 1
}

conflict_hits=$(git grep -nE '^(<<<<<<< |>>>>>>> )' -- . || true)
if [ -n "$conflict_hits" ]; then
  echo "guardrails_check: unresolved conflict markers detected:" >&2
  printf '%s\n' "$conflict_hits" >&2
  exit 1
fi

todo_hits=$(
  git grep -nEI '(^|[[:space:]])(//|#|/\*+|\*|<!--)[[:space:]]*(TODO|FIXME)\b' -- \
    '*.go' '*.py' '*.rs' '*.ts' '*.tsx' '*.js' '*.jsx' '*.mjs' '*.sh' '*.yaml' '*.yml' 'Makefile' \
    || true
)
if [ -n "$todo_hits" ]; then
  allowed_issue_re='\b(TODO|FIXME)\((bd|br)-[0-9]+\)'
  invalid_todos=$(printf '%s\n' "$todo_hits" | grep -Eiv "$allowed_issue_re" || true)
  if [ -n "$invalid_todos" ]; then
    echo "guardrails_check: TODO/FIXME entries require issue IDs (e.g. TODO(bd-123)):" >&2
    printf '%s\n' "$invalid_todos" >&2
    exit 1
  fi
fi

echo "guardrails_check: ok"

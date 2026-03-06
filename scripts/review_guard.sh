#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
common_git_dir=$(git rev-parse --git-common-dir)
if [ -d "$repo_root/$common_git_dir" ]; then
  common_git_dir_abs=$(cd "$repo_root/$common_git_dir" && pwd)
else
  common_git_dir_abs=$(cd "$common_git_dir" && pwd)
fi
primary_checkout=$(dirname "$common_git_dir_abs")
repo_name=$(basename "$primary_checkout")
cd "$repo_root"

base_ref=${REVIEW_BASE_REF:-}
if [ -z "$base_ref" ] && [ -n "${GITHUB_BASE_REF:-}" ]; then
  base_ref="origin/${GITHUB_BASE_REF}"
fi
if [ -z "$base_ref" ]; then
  base_ref=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null || true)
fi
if [ -z "$base_ref" ]; then
  base_ref="HEAD~1"
fi

changed_files=$(git diff --name-only "$base_ref...HEAD" 2>/dev/null || git diff --name-only HEAD~1...HEAD 2>/dev/null || true)
if [ -z "$changed_files" ]; then
  echo "review_guard: no changed files against $base_ref"
  exit 0
fi

source_re='\.(go|py|rs|ts|tsx|js|jsx|mjs)$'
test_re='(^|/)(tests?/|__tests__/)|(_test\.go$)|(\.test\.)|(\.spec\.)'

case "$repo_name" in
  assistant)
    risky_re='(^|/)(internal/(agent|knowledge|matrix|git|temporal)|cmd/|memory/)'
    ;;
  chum|chum-v2|chum-automation|cortex|cortex-factory)
    risky_re='(^|/)(internal/|cmd/|scripts/|build/)'
    ;;
  hg-chum-integration|hg-chum-integration-automation)
    risky_re='(^|/)(internal/(activities|api|db|schedules|workflows)|collectors/|web/)'
    ;;
  hg-reporting)
    risky_re='(^|/)(lib/|config/|hg-report/)|(^|/)(hg_|fetch-|pull_).+\.py$'
    ;;
  kalshi-poly-arb)
    risky_re='(^|/)(arb-detector|cross-venue-scanner|execution-engine|market-registry|parity-engine|reconciler|signer-service|trader-core|treasury-manager|shared)/'
    ;;
  golf-directory|cfo|hg-website)
    risky_re='(^|/)(app/|src/|components/|lib/|scripts/).+\.(ts|tsx|js|jsx)$'
    ;;
  *)
    risky_re='(^|/)(cmd/|internal/|lib/|src/|scripts/|collectors/|app/).+\.(go|py|rs|ts|tsx|js|jsx|mjs)$'
    ;;
esac

source_changed=$(printf '%s\n' "$changed_files" | grep -E "$source_re" || true)
test_changed=$(printf '%s\n' "$changed_files" | grep -E "$test_re" || true)
risky_changed=$(printf '%s\n' "$changed_files" | grep -E "$risky_re" || true)

printf 'review_guard: changed files against %s\n' "$base_ref"
printf '%s\n' "$changed_files"

if [ -n "$risky_changed" ] && [ -z "$test_changed" ] && [ -z "${REVIEW_GUARD_ALLOW_NO_TESTS:-}" ]; then
  echo "review_guard: high-risk changes detected without corresponding test changes" >&2
  printf '%s\n' "$risky_changed" >&2
  echo "review_guard: add/update tests or set REVIEW_GUARD_ALLOW_NO_TESTS=1 with justification" >&2
  exit 1
fi

if [ -n "$source_changed" ]; then
  cat <<'CHECKLIST'
review_guard checklist:
- state advancement vs dropped work
- retry/backoff/timeout semantics
- queueing/concurrency/rate limiting
- auth/permissions/config boundaries
- migrations/schema/data compatibility
CHECKLIST
fi

echo "review_guard: ok"

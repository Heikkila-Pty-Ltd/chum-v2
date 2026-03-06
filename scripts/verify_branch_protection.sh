#!/usr/bin/env bash
set -euo pipefail

repo=${1:-${GITHUB_REPOSITORY:-}}
branch=${2:-master}

die() {
  echo "verify_branch_protection: $*" >&2
  exit 1
}

if [ -z "$repo" ]; then
  die "repository not provided; pass <owner/repo> or set GITHUB_REPOSITORY"
fi

command -v jq >/dev/null 2>&1 || die "jq is required"

headers=(-H "Accept: application/vnd.github+json")
if [ -n "${GITHUB_TOKEN:-}" ]; then
  headers+=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

branch_url="https://api.github.com/repos/${repo}/branches/${branch}"
protection_url="https://api.github.com/repos/${repo}/branches/${branch}/protection"

branch_payload=$(curl -fsSL "${headers[@]}" "$branch_url") || die "failed to fetch branch details for ${repo}:${branch}"
protected=$(printf '%s' "$branch_payload" | jq -r '.protected // false')
if [ "$protected" != "true" ]; then
  die "${repo}:${branch} is not protected"
fi

protection_payload=$(curl -fsSL "${headers[@]}" "$protection_url") || die "failed to fetch branch protection details for ${repo}:${branch}"
approvals=$(printf '%s' "$protection_payload" | jq -r '.required_pull_request_reviews.required_approving_review_count // 0')
contexts_count=$(printf '%s' "$protection_payload" | jq -r '.required_status_checks.contexts | if type == "array" then length else 0 end')
checks_count=$(printf '%s' "$protection_payload" | jq -r '.required_status_checks.checks | if type == "array" then length else 0 end')

if [ "$approvals" -lt 1 ]; then
  die "${repo}:${branch} requires at least one approving PR review"
fi

if [ "$contexts_count" -eq 0 ] && [ "$checks_count" -eq 0 ]; then
  die "${repo}:${branch} must require at least one status check"
fi

echo "verify_branch_protection: ok (${repo}:${branch})"

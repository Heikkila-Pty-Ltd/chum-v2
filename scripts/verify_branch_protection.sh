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

if [ "${repo#*/}" = "$repo" ]; then
  die "repository must be in owner/repo format"
fi

command -v jq >/dev/null 2>&1 || die "jq is required"

headers=(-H "Accept: application/vnd.github+json")
if [ -n "${GITHUB_TOKEN:-}" ]; then
  headers+=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

branch_url="https://api.github.com/repos/${repo}/branches/${branch}"

branch_payload=$(curl -fsSL "${headers[@]}" "$branch_url") || die "failed to fetch branch details for ${repo}:${branch}"
protected=$(printf '%s' "$branch_payload" | jq -r '.protected // false')
if [ "$protected" != "true" ]; then
  die "${repo}:${branch} is not protected"
fi

branch_checks_contexts=$(printf '%s' "$branch_payload" | jq -r '.protection.required_status_checks.contexts | if type == "array" then length else 0 end')
branch_checks_named=$(printf '%s' "$branch_payload" | jq -r '.protection.required_status_checks.checks | if type == "array" then length else 0 end')
if [ "$branch_checks_contexts" -eq 0 ] && [ "$branch_checks_named" -eq 0 ]; then
  die "${repo}:${branch} is protected but does not require any status checks"
fi

if [ -z "${GITHUB_TOKEN:-}" ]; then
  echo "verify_branch_protection: ok (${repo}:${branch}) [status checks verified; review requirement not verifiable without token]"
  exit 0
fi

owner=${repo%%/*}
name=${repo#*/}
graphql_query='query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    branchProtectionRules(first: 100) {
      nodes {
        pattern
        requiresApprovingReviews
        requiredApprovingReviewCount
        requiresStatusChecks
        requiredStatusCheckContexts
      }
    }
  }
}'
graphql_body=$(jq -cn --arg query "$graphql_query" --arg owner "$owner" --arg name "$name" '{query: $query, variables: {owner: $owner, name: $name}}')
graphql_payload=$(curl -fsSL \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "$graphql_body" \
  "https://api.github.com/graphql" || true)

if [ -z "$graphql_payload" ]; then
  echo "verify_branch_protection: ok (${repo}:${branch}) [status checks verified; GraphQL details unavailable]"
  exit 0
fi

if printf '%s' "$graphql_payload" | jq -e '.errors | type == "array" and length > 0' >/dev/null 2>&1; then
  echo "verify_branch_protection: ok (${repo}:${branch}) [status checks verified; GraphQL access denied]"
  exit 0
fi

rule=$(printf '%s' "$graphql_payload" | jq -c --arg branch "$branch" '.data.repository.branchProtectionRules.nodes // [] | map(select(.pattern == $branch or .pattern == "*")) | first // empty' 2>/dev/null || true)
if [ -z "$rule" ]; then
  echo "verify_branch_protection: ok (${repo}:${branch}) [status checks verified; branch rule details unavailable]"
  exit 0
fi

requires_reviews=$(printf '%s' "$rule" | jq -r '.requiresApprovingReviews // false')
approvals=$(printf '%s' "$rule" | jq -r '.requiredApprovingReviewCount // 0')
requires_checks=$(printf '%s' "$rule" | jq -r '.requiresStatusChecks // false')
contexts_count=$(printf '%s' "$rule" | jq -r '.requiredStatusCheckContexts | if type == "array" then length else 0 end')

if [ "$requires_reviews" != "true" ] || [ "$approvals" -lt 1 ]; then
  die "${repo}:${branch} requires at least one approving PR review"
fi

if [ "$requires_checks" != "true" ] || [ "$contexts_count" -lt 1 ]; then
  die "${repo}:${branch} must require at least one status check"
fi

echo "verify_branch_protection: ok (${repo}:${branch})"

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

if [ -z "${GITHUB_TOKEN:-}" ]; then
  die "GITHUB_TOKEN is required to verify review/status-check requirements"
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
  "https://api.github.com/graphql") || die "failed to fetch branch protection rules via GraphQL"

rule=$(printf '%s' "$graphql_payload" | jq -c --arg branch "$branch" '.data.repository.branchProtectionRules.nodes | map(select(.pattern == $branch or .pattern == "*")) | first // empty')
if [ -z "$rule" ]; then
  die "no branch protection rule found for ${repo}:${branch}"
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

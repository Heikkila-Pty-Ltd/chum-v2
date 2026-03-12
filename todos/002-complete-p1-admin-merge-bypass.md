---
status: pending
priority: p1
issue_id: "002"
tags: [code-review, security, review-pipeline]
dependencies: ["001"]
---

# Autonomous Admin Merge Bypasses Branch Protection

## Problem Statement

`MergePRActivity` in `review.go:231-247` automatically escalates to `gh pr merge --admin` when a normal merge is blocked by branch protection. Combined with the signal parsing bypass (issue 001), this creates a complete trust chain bypass: malicious/wrong code gets auto-approved by the LLM reviewer and force-merged past all branch protection rules.

## Findings

- Normal merge attempt at line 232: `gh pr merge ... --squash --delete-branch`
- If `isBaseBranchPolicyBlocked(err)` → automatic escalation to `--admin` merge (line 240)
- `--admin` bypasses: required reviewers, status checks, branch restrictions
- No configuration flag to disable this behavior
- No human gate before admin merge

## Proposed Solutions

### Option A: Remove admin merge entirely (Recommended)
When merge is blocked by branch protection, close as `needs_review` with `merge_blocked`. Let a human resolve it.

**Pros:** Respects branch protection as intended, fail-safe
**Cons:** Tasks that would have auto-merged now need human intervention
**Effort:** Small
**Risk:** Low

### Option B: Config flag for admin merge
Add `allow_admin_merge` per-project config (default false). Only attempt `--admin` when explicitly enabled.

**Pros:** Flexible for projects that intentionally want autonomous merging
**Cons:** Easy to enable accidentally; still bypasses safety
**Effort:** Small
**Risk:** Medium

## Technical Details

**Affected files:**
- `internal/engine/review.go` — `MergePRActivity` (lines 224-278)

## Acceptance Criteria

- [ ] `--admin` merge removed or gated behind explicit config (default off)
- [ ] When branch protection blocks merge, task ends as `needs_review` with `merge_blocked`
- [ ] Test coverage for blocked merge → needs_review path

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Identified during orchestration review | Admin merge + self-review = complete bypass |

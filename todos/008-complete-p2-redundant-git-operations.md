---
status: complete
priority: p2
issue_id: "008"
tags: [code-review, performance, git]
dependencies: []
---

# Redundant Git Operations — Double Fetch, Triple Slug Resolution, Duplicate Review Queries

## Problem Statement

Multiple git and GitHub API operations are duplicated per task lifecycle:
1. `git fetch origin` runs in `pullMaster` (dispatcher) AND again in `SetupWorktreeAtRef` (agent)
2. `repoSlugFromWorkDir` shells out to `git remote get-url origin` 2-3 times per review round for the same static value
3. `listPRReviews` called in both `SubmitReviewActivity` and `CheckPRStateActivity` per round
4. Worktree setup spawns 8-12 sequential git subprocesses where some could be combined

## Findings

- `dispatcher.go:583` — `git fetch --prune origin` per project per tick
- `git.go:73` — `git fetch origin <branch>` per task dispatch (redundant after pullMaster)
- `review.go:668` — `git remote get-url origin` called from Submit, List, ReadFeedback, UpdateBranch
- `review.go:103,161` — `listPRReviews` called by both Submit and CheckPRState per round
- `git.go:103-111` — 3 sequential `git config` calls that could be one

## Proposed Solutions

- Cache repo slug per worktree path (in-memory, lifetime of activity)
- Skip fetch in SetupWorktreeAtRef if caller signals origin is fresh
- Thread review list from Submit to CheckPRState
- Combine git config writes

**Effort:** Small per item | **Risk:** Low

## Acceptance Criteria

- [ ] No duplicate `git fetch origin` per task dispatch
- [ ] `repoSlugFromWorkDir` cached or passed as parameter
- [ ] `listPRReviews` called once per review round, not twice

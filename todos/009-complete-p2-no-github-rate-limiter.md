---
status: complete
priority: p2
issue_id: "009"
tags: [code-review, performance, github-api]
dependencies: []
---

# No Global GitHub API Rate Limiter Across Concurrent Workflows

## Problem Statement

Multiple concurrent agent workflows make GitHub API calls (`gh pr diff`, `gh api repos/.../reviews`, `gh pr view`, `gh pr merge`, `gh api user`, `gh pr create`) with no shared rate limiter. With MaxConcurrent tasks running, each doing 2 review rounds with 4-6 API calls each, the system can easily exhaust GitHub's 5,000 requests/hour authenticated limit.

## Proposed Solutions

### Option A: Shared rate.Limiter (Recommended)
Use `golang.org/x/time/rate` Limiter shared across all `gh` calls. Set to ~80% of GitHub's rate limit.

**Effort:** Medium | **Risk:** Low

## Acceptance Criteria

- [ ] All `gh` and `gh api` calls pass through a shared rate limiter
- [ ] System does not hit GitHub API rate limits under max concurrent load

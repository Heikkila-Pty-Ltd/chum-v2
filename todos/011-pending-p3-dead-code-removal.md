---
status: complete
priority: p3
issue_id: "011"
tags: [code-review, quality, dead-code]
dependencies: []
---

# Dead Code and Unused Functions (~71 LOC)

## Problem Statement

Multiple unused functions and dead code paths identified across the engine:

| File | Item | LOC |
|------|------|-----|
| `activities.go:216-218` | `CreatePRActivity` — superseded by `CreatePRInfoActivity` | 3 |
| `activities.go:379-381` | `buildCodebaseContext()` — unused wrapper | 3 |
| `worker.go:143` | Registration of dead `CreatePRActivity` | 1 |
| `review.go:327-329` | `resolveReviewer()` — test-only wrapper, production uses `resolveReviewerWithStage` | 3 |
| `tier.go:43-66` | `RetriesForTier()`, `NextTier()` — tier escalation never wired | 24 |
| `classify.go:178-196` | `transientPatterns`, `IsTransientInfraFailure()` — unused in production | 19 |
| `activities.go:174-182` | `runCommandCombinedOutput` — duplicate of `runCommand` | 9 |
| `dispatcher.go:666-674` | `runGitCommand` — duplicate of `runCommand` with hardcoded "git" | 9 |
| `types.go:83` | `CloseFailed` — no workflow produces this close reason | 1 |

## Acceptance Criteria

- [ ] All listed dead code removed
- [ ] Callers of `runCommandCombinedOutput` and `runGitCommand` updated to use `runCommand`
- [ ] All tests still pass

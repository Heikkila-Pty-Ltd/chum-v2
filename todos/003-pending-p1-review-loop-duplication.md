---
status: complete
priority: p1
issue_id: "003"
tags: [code-review, architecture, duplication]
dependencies: []
---

# Review Loop Duplicated Between AgentWorkflow and ReviewWorkflow (~200 LOC)

## Problem Statement

The review loop in `AgentWorkflow` (`agent.go:276-473`) and `ReviewWorkflow` (`review_workflow.go:134-336`) are near-identical: RunReview → GuardClean → SubmitReview → CheckPRState → switch(Outcome). This has already caused a behavioral divergence: `ReviewWorkflow` lacks trace recording and failure classification that `AgentWorkflow` has. Any future bug fix must be applied in two places.

## Findings

- ~200 lines of duplicated control flow
- Only differences: `closeAndTrace` vs `closeAndNotify`, token accumulation, TaskRequest construction
- `ReviewWorkflow` missing: trace recording, failure classification, token/cost tracking
- Both have identical: timeout setup (~30 LOC), worktree cleanup defer (~10 LOC), feedback extraction (~12 LOC)
- Pattern agents confirmed this as the highest-severity duplication in the engine

## Proposed Solutions

### Option A: Extract shared reviewLoop function (Recommended)
```go
func reviewLoop(ctx workflow.Context, opts reviewLoopOpts) (CloseDetail, error)
```
Both workflows call this with their specific close function as a callback.

**Pros:** Single source of truth, eliminates ~120 LOC, ensures both paths have same behavior
**Cons:** Slightly more complex function signature
**Effort:** Medium
**Risk:** Low (pure refactor, needs Temporal version gate)

### Option B: Make ReviewWorkflow a child of AgentWorkflow
Remove ReviewWorkflow entirely; have the dispatcher re-dispatch failed review tasks through AgentWorkflow with a "skip execution" flag.

**Pros:** Only one workflow to maintain
**Cons:** Larger change, version gate complexity, different failure semantics
**Effort:** Large
**Risk:** Medium

## Technical Details

**Affected files:**
- `internal/engine/agent.go` — review loop (lines 276-473)
- `internal/engine/review_workflow.go` — review loop (lines 134-336)
- New: shared helper function (either in agent.go or a new review_loop.go)

## Acceptance Criteria

- [x] Single review loop implementation used by both workflows
- [x] ReviewWorkflow gains trace recording (currently missing)
- [x] All existing agent_workflow_test.go and review tests pass
- [x] Temporal version gate added for replay safety

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Identified by architecture + pattern + simplicity reviewers | All 3 agents flagged this as highest priority |
| 2026-03-12 | Extracted shared reviewLoop into review_loop.go | ~200 LOC deduped, ReviewWorkflow now has trace recording via version gate |

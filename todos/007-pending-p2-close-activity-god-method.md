---
status: pending
priority: p2
issue_id: "007"
tags: [code-review, architecture, separation-of-concerns]
dependencies: []
---

# CloseTaskWithDetailActivity Is a 120-Line God Method

## Problem Statement

`CloseTaskWithDetailActivity` in `activities.go:247-366` mixes DAG persistence, beads bridge outbox projection, and legacy direct beads writeback in a single activity. It also has a cross-boundary caller: `ScanOrphanedReviewsActivity` in `dispatcher.go:822-834` constructs a partial `Activities` struct (missing LLM, AST, Traces, Perf, ChatSend) to call it directly as a plain Go function, bypassing Temporal's activity execution guarantees.

## Findings

- 120 lines mixing: error_log preservation, JSON marshal, DAG update, beads mapping resolution, bridge outbox enqueue, direct beads writeback with Close/Update per status
- `da.DAG.(*dag.DAG)` type assertion used 5+ times across the codebase to access bridge methods
- `ScanOrphanedReviewsActivity` constructs partial `Activities{DAG, Config, Logger, BeadsClients}` — nil pointer risk if CloseTaskWithDetailActivity later uses missing fields

## Proposed Solutions

### Option A: Split into closeDAGTask + projectToBeads (Recommended)
Extract beads writeback into a separate function or activity. CloseTaskWithDetailActivity only updates DAG.

**Effort:** Medium | **Risk:** Low

### Option B: Shared close function callable by both activity structs
Extract core close logic into a standalone function that both `Activities` and `DispatchActivities` can call.

**Effort:** Medium | **Risk:** Low

## Acceptance Criteria

- [ ] Close activity has single responsibility (DAG update)
- [ ] Beads writeback in separate function/activity
- [ ] No partial `Activities` struct construction in dispatcher
- [ ] `TaskStore` interface widened or `BridgeStore` interface composed to eliminate type assertions

---
status: pending
priority: p2
issue_id: "005"
tags: [code-review, performance, dispatcher]
dependencies: []
---

# Dispatcher Scans All Tasks Every Tick — O(N) Full Table Scan

## Problem Statement

`suppressRedundantParentDispatch` in `dispatcher.go:400` calls `ListTasks` with no status filter, deserializing every task row (including JSON fields) per project per tick. `filterBeadsMappedReadyTasks` at line 449 issues N+1 individual queries per ready task.

## Findings

- `ListTasks` executes `SELECT * FROM tasks WHERE project = ?` — all statuses, all fields
- Every row deserializes JSON `labels` and `metadata` fields
- At 5000 tasks/project with 30s ticks → 5000 rows deserialized every 30s
- `filterBeadsMappedReadyTasks` issues individual `GetBeadsMappingByTask` query per ready task + potential `bc.Show` network call

## Proposed Solutions

### Option A: Targeted COUNT query (Recommended)
Replace `ListTasks` with `SELECT parent_id, COUNT(*) FROM tasks WHERE project = ? AND parent_id != '' GROUP BY parent_id`.

**Effort:** Small | **Risk:** Low

### Option B: Batch mapping lookup
Replace N+1 `GetBeadsMappingByTask` calls with single `SELECT ... WHERE task_id IN (...)`.

**Effort:** Small-Medium | **Risk:** Low

## Acceptance Criteria

- [ ] `suppressRedundantParentDispatch` does not load full task rows
- [ ] `filterBeadsMappedReadyTasks` uses batch query instead of N+1
- [ ] Dispatcher tick time decreases measurably with large task counts

---
status: complete
priority: p3
issue_id: "014"
tags: [code-review, operations, disk-space]
dependencies: []
---

# No Global Worktree Orphan Cleanup

## Problem Statement

Worktrees are created in `/tmp/chum-worktrees/<taskID>`. Cleanup runs on every workflow exit, but if the Temporal worker crashes, orphaned worktrees persist indefinitely. There is no periodic sweep. For a 500MB repo, 10 orphaned worktrees waste 5GB of disk.

## Proposed Solutions

Add a periodic cleanup activity in the dispatcher that removes worktrees for tasks in terminal states (completed, failed, needs_review) older than 24h.

**Effort:** Small | **Risk:** Low

## Acceptance Criteria

- [ ] Periodic sweep removes orphaned worktrees for completed/failed tasks
- [ ] Active worktrees for running tasks are never touched

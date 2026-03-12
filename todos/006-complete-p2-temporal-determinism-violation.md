---
status: complete
priority: p2
issue_id: "006"
tags: [code-review, architecture, temporal]
dependencies: []
---

# os.TempDir() Called Inside Workflow Code — Temporal Determinism Violation

## Problem Statement

Both `AgentWorkflow` (`agent.go:78`) and `ReviewWorkflow` (`review_workflow.go:67`) call `os.TempDir()` inside workflow code to construct the worktree cleanup path. `os.TempDir()` reads the `TMPDIR` environment variable — a non-deterministic operation that can produce different results during Temporal replay if the worker environment changes.

## Findings

- `agent.go:78`: `predictableWorktreePath := filepath.Join(os.TempDir(), "chum-worktrees", req.TaskID)`
- `review_workflow.go:67`: identical pattern
- During replay on a different worker or after restart with different env, path could differ
- Cleanup activity would receive wrong path argument
- Currently works because TMPDIR is stable and cleanup errors are suppressed

## Proposed Solutions

### Option A: Derive from activity output (Recommended)
`SetupWorktreeActivity` already returns `worktreePath`. Derive the cleanup path from it (it's the same value). Store it after the activity completes.

**Effort:** Small | **Risk:** Low

## Acceptance Criteria

- [ ] No `os.TempDir()` calls inside workflow functions
- [ ] Cleanup path derived from activity-returned worktree path
- [ ] Workflow replay produces identical behavior regardless of environment

---
status: complete
priority: p2
issue_id: "010"
tags: [code-review, security, input-validation]
dependencies: []
---

# TaskID Used Unsanitized in Branch Names and File Paths

## Problem Statement

`git.go:28-29` uses task ID directly in branch names (`chum/{taskID}`) and filesystem paths (`/tmp/chum-worktrees/{taskID}`) without validation. If task IDs from beads bridge contain path traversal (`../`), shell metacharacters, or git-invalid characters, this could cause path traversal or unexpected git behavior.

## Proposed Solutions

Validate task IDs against `^[a-zA-Z0-9._-]+$` before use in branch names or paths. Reject tasks with invalid IDs at dispatch time.

**Effort:** Small | **Risk:** Low

## Acceptance Criteria

- [ ] Task IDs validated against strict alphanumeric+dash+dot+underscore pattern
- [ ] Tasks with invalid IDs rejected at dispatch with clear error

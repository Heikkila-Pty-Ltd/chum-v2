---
status: pending
priority: p3
issue_id: "012"
tags: [code-review, quality, duplication]
dependencies: ["003"]
---

# Timeout Setup and ActivityOptions Duplicated (~30 LOC x2)

## Problem Statement

Identical 30-line timeout defaulting and ActivityOptions construction blocks in `agent.go:36-68` and `review_workflow.go:32-64`. Same defaults (2m short, 45m exec, 10m review) hardcoded in both locations.

## Proposed Solutions

Extract `buildActivityOpts(shortTimeout, execTimeout, reviewTimeout)` returning the 4 option structs. Define default constants in `types.go`.

**Effort:** Small | **Risk:** Low

## Acceptance Criteria

- [ ] Single source of timeout defaults
- [ ] Single function building ActivityOptions used by both workflows

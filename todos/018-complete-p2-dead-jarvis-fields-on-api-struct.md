---
status: pending
priority: p2
issue_id: "018"
tags: [code-review, dead-code, backend, cleanup]
dependencies: []
---

# Dead JarvisKBPath and jarvisDB fields on API struct

## Problem Statement

`internal/jarvis/api.go:36-37` still has `JarvisKBPath string` and `jarvisDB *sql.DB` fields on the API struct. The Jarvis KB handlers were deleted (`dashboard_jarvis.go` removed), but the struct fields and any wiring that sets `JarvisKBPath` in dashboard-preview command were left behind.

## Findings

- **Location:** `internal/jarvis/api.go:36-37`
- **Impact:** Dead code that confuses future readers into thinking Jarvis KB is still operational
- **Also check:** `dashboard-preview` command wiring that sets `JarvisKBPath`

## Proposed Solutions

### Solution 1: Remove both fields and any wiring

Delete `JarvisKBPath` and `jarvisDB` from the API struct. Grep for any code that sets `JarvisKBPath` on API construction (likely in cmd/ or main) and remove those assignments.

**Effort:** Small | **Risk:** Low — compile will catch any remaining references

## Acceptance Criteria

- [ ] `JarvisKBPath` and `jarvisDB` removed from API struct
- [ ] No remaining code assigns to these fields
- [ ] `go build ./...` passes

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-24 | Identified by architecture-strategist agent | Leftover from Jarvis KB deletion |

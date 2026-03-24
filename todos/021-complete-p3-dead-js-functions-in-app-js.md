---
status: pending
priority: p3
issue_id: "021"
tags: [code-review, dead-code, javascript, cleanup]
dependencies: []
---

# Dead JS functions and constants in app.js

## Problem Statement

Several functions and constants in `web/app.js` are no longer referenced after the dashboard rework:

- `bindActionButton` (lines 154-186) — was used by old overview.js, new overview.js uses inline handlers
- `renderTextList` (lines 188-191) — unused
- `healthColor` (lines 130-134) — was used by old overview, replaced by health strip cards
- `STATUS_KANBAN_MAP` (lines 84-91) — kanban board is gone
- `FAILED_STATUSES` (line 82) — check if still referenced; may be dead

## Findings

- **Location:** `web/app.js:82-191` (scattered)
- **Volume:** ~55 lines of dead code
- **Identified by:** code-simplicity-reviewer agent

## Proposed Solutions

### Solution 1: Grep and delete

For each function/constant, grep all `.js` and `.html` files. If zero references outside the definition, delete it.

**Effort:** Small | **Risk:** Low — grep confirms before delete

## Acceptance Criteria

- [ ] Each function/constant verified as unused before removal
- [ ] Dead functions removed
- [ ] Dashboard still works after cleanup
- [ ] `FAILED_STATUSES` kept if still referenced (check overview.js retry logic)

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-24 | Identified by code-simplicity-reviewer agent | Leftover from old overview kanban + action button pattern |

---
status: pending
priority: p3
issue_id: "020"
tags: [code-review, dead-code, css, cleanup]
dependencies: []
---

# ~408 lines of dead .structure-* and .tree-* CSS

## Problem Statement

`web/style.css` lines 440-848 contain CSS selectors for `.structure-*` and `.tree-*` classes from the deleted `structure.js` view. These selectors are no longer referenced by any HTML or JS in the codebase. Additionally, `.ov-action-danger` and other old overview selectors may still be present.

## Findings

- **Location:** `web/style.css:440-848` (approximate range)
- **Volume:** ~408 lines of dead CSS
- **Identified by:** code-simplicity-reviewer and architecture-strategist agents
- **Root cause:** structure.js was replaced by tasks.js but its CSS block was not removed

## Proposed Solutions

### Solution 1: Delete the dead CSS blocks

Remove all `.structure-*`, `.tree-*`, and any remaining `.ov-kanban-*` or `.ov-action-danger` selectors. Grep for each class in JS/HTML first to confirm it's dead.

**Effort:** Small | **Risk:** Low — verify no classes are still referenced before deleting

## Acceptance Criteria

- [ ] All `.structure-*` selectors removed
- [ ] All `.tree-*` selectors removed
- [ ] Any other orphaned selectors from old views removed
- [ ] Dashboard renders correctly after cleanup

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-24 | Identified by code-simplicity-reviewer agent | ~408 lines of dead CSS from deleted structure.js |

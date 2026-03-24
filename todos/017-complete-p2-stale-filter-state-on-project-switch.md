---
status: pending
priority: p2
issue_id: "017"
tags: [code-review, ux, dashboard, tasks]
dependencies: []
---

# Stale filter/sort state on project switch in tasks view

## Problem Statement

In `web/views/tasks.js`, the closure variables `filterStatus`, `filterText`, `sortCol`, and `sortAsc` persist across project switches. When the user switches from project A (filtered to "failed") to project B, they see project B pre-filtered to "failed" — confusing because the filter UI shows a stale selection they didn't choose for this project.

This is a known pattern from a prior dashboard bug documented in `docs/solutions/ui-bugs/`.

## Findings

- **Location:** `web/views/tasks.js:7-10` (closure variables), `render()` at line 20
- **Root cause:** `render(viewport, project)` does not reset filter state before loading new project data
- **Prior art:** Learnings researcher flagged this exact pattern from a previous dashboard incident

## Proposed Solutions

### Solution 1: Reset filters on render (Recommended)

At the top of `render()`, reset filter state:

```js
function render(viewport, project) {
  filterStatus = '';
  filterText = '';
  // sortCol/sortAsc can optionally persist — sorting is less confusing than filtering
  viewport.innerHTML = '<div class="loading-state">loading…</div>';
  // ...
}
```

**Pros:** Simple, prevents confusion. **Cons:** User loses filters when switching projects (acceptable trade-off).
**Effort:** Small | **Risk:** None

## Acceptance Criteria

- [ ] Filter state resets when switching projects
- [ ] Sort state either resets or persists (design choice, both acceptable)
- [ ] Filter dropdown and search input reflect reset state in the UI

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-24 | Identified by learnings-researcher and code-simplicity agents | Known pattern from prior dashboard bug |

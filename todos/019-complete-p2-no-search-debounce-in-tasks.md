---
status: pending
priority: p2
issue_id: "019"
tags: [code-review, performance, ux, dashboard]
dependencies: []
---

# No debounce on task search input

## Problem Statement

`web/views/tasks.js:218` fires `input` handler on every keystroke, re-rendering the entire table each time. With hundreds of tasks, this causes visible jank and layout thrashing.

## Findings

- **Location:** `web/views/tasks.js:217-228`
- **Impact:** Performance degrades linearly with task count. Typing "running" triggers 7 full table re-renders.
- **Identified by:** performance-oracle agent

## Proposed Solutions

### Solution 1: Simple debounce (Recommended)

```js
let searchTimer;
if (search) {
  search.addEventListener('input', () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      filterText = search.value;
      if (activeTab === 'table') {
        viewport.querySelector('.tasks-content').innerHTML = renderTable(applyFilters(currentData.tasks));
        viewport.querySelectorAll('.task-row').forEach(row => {
          row.addEventListener('click', () => App.openPanel(row.dataset.taskId));
        });
      }
    }, 200);
  });
}
```

**Effort:** Small | **Risk:** None

## Acceptance Criteria

- [ ] Search input debounced (150-300ms)
- [ ] Table only re-renders after user pauses typing

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-24 | Identified by performance-oracle agent | Standard UX pattern for search inputs |

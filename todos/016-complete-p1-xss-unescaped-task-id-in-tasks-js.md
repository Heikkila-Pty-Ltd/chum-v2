---
status: pending
priority: p1
issue_id: "016"
tags: [code-review, security, xss, dashboard]
dependencies: []
---

# XSS: Unescaped task ID in tasks.js data attribute

## Problem Statement

In `web/views/tasks.js:97`, `t.id` is interpolated directly into a `data-task-id` HTML attribute without escaping:

```js
<tr class="task-row" data-task-id="${t.id}">
```

Task IDs are user-influenced (they come from beads bridge issue IDs or plan decomposition). A crafted ID containing `"` could break out of the attribute and inject arbitrary HTML/JS. This is the only confirmed XSS in the dashboard rework.

## Findings

- **Location:** `web/views/tasks.js:97`
- **Vector:** Task IDs originate from external sources (GitHub issues via beads bridge, plan materialization)
- **Impact:** Attribute injection → potential script execution in the dashboard context
- **Severity:** P1 — the dashboard runs on localhost but still processes external data

## Proposed Solutions

### Solution 1: Escape with `App.escapeHtml()` (Recommended)

```js
<tr class="task-row" data-task-id="${App.escapeHtml(t.id)}">
```

**Pros:** One-line fix, consistent with how `t.title` and `t.project` are already escaped on lines 99-100.
**Effort:** Small | **Risk:** None

## Acceptance Criteria

- [ ] `t.id` in the `data-task-id` attribute is escaped via `App.escapeHtml()`
- [ ] Verify no other unescaped ID interpolations exist in tasks.js or overview.js

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-24 | Identified by security-sentinel review agent | Only confirmed XSS in dashboard rework |

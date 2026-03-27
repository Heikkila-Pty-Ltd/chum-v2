---
title: "feat: Work Page — Task & Epic Visibility"
type: feat
status: completed
date: 2026-03-24
origin: docs/brainstorms/2026-03-24-work-page-task-visibility-brainstorm.md
---

# feat: Work Page — Task & Epic Visibility

## Overview

Replace the Projects page (nav slot 5) with a master-detail Work page that lets you examine what's being built and why. Left panel: project picker + collapsible epic/task/subtask tree with progress bars and a filter bar. Right panel: full task detail — narrative, decisions with UCT scores, relationships, execution metrics, error logs, traces.

Cross-page deep links from Check feed and Steer queue navigate directly to `#/work/{taskId}` with the target task auto-expanded and selected.

(see brainstorm: `docs/brainstorms/2026-03-24-work-page-task-visibility-brainstorm.md`)

## Problem Statement

The dashboard has 5 pages full of summary metrics but no way to examine individual tasks. You can't read a task's description, see why the UCT chose a path, understand epic-to-subtask progress, view error logs, or inspect execution traces. The Projects page shows a minimal tree (title + status badge + truncated ID) where clicking does nothing. The backend already serves all this data via `/api/dashboard/task/{id}` and `/api/dashboard/tree/{project}` — the frontend just never calls them.

## Proposed Solution

A two-column Work page following the master-detail pattern (file explorer / email client layout):

```
┌─────────────────────────────────────────────────┐
│ Nav: Check  Plan  Steer  Learn  [Work]          │
├──────────────────┬──────────────────────────────┤
│ [project-a    ▼] │  Task: fix-auth-bug          │
│ ┌filter...─────┐ │  ─────────────────────────── │
│ ▼ Epic: Auth ██░ │  Description: ...            │
│   ├ fix-login ● │  Acceptance: ...             │
│   ├ add-2fa   ● │  ─────────────────────────── │
│   └ tests     ○ │  Decisions:                  │
│ ▶ Epic: Billing  │   → chose JWT (UCT: 0.82)   │
│ ▶ Epic: Search   │  ─────────────────────────── │
│                  │  Parent: Epic: Auth          │
│                  │  Children: 0 │ Deps: 2       │
│                  │  Targets: auth/login.go:42   │
│                  │  ▸ Execution (collapsible)   │
│                  │  ▸ Error Log (collapsible)   │
│                  │  ▸ Traces (collapsible)      │
└──────────────────┴──────────────────────────────┘
```

## Technical Approach

### Architecture

**No new backend endpoints needed.** The frontend wires up existing APIs:

| API | Purpose | Already in `web/app.js` |
|-----|---------|------------------------|
| `GET /api/dashboard/projects` | Populate project picker | `API.projects()` line 27 |
| `GET /api/dashboard/tree/{project}` | Build left-panel tree | `API.tree(p)` line 29 |
| `GET /api/dashboard/task/{id}` | Populate right-panel detail | `API.task(id)` line 31 |
| `GET /api/dashboard/traces/{id}` | Execution traces (secondary) | `API.traces(id)` line 39 |

**Files to create:**
- `web/views/work.js` — Alpine.js component (`workPage`), ~400-500 lines
- CSS in `web/style.css` — `.work-*` namespace, ~200 lines

**Files to modify:**
- `web/index.html` — nav link `Projects → Work`, add `<script src="/views/work.js">`
- `web/app.js` — update `ROUTE_REDIRECTS` (`projects: 'work'`, `tasks: 'work'`, `structure: 'work'`), pass `param` to view render
- `web/views/check.js` — task IDs become `<a href="#/work/{id}">` links
- `web/views/steer.js` — task IDs become `<a href="#/work/{id}">` links

**Files to remove:**
- `web/views/projects.js` — replaced entirely

### Key Technical Decisions

**URL format: `#/work/{taskId}`** — fits the existing `parseHash()` which already extracts `parts[1]` as `param`. No router rewrite needed. (see spec-flow analysis: `parseHash` at `app.js:190-196`)

**Deep link resolution flow:**
1. Parse `taskId` from URL
2. Fetch `API.task(taskId)` → extract `task.project`
3. Set project picker to that project
4. Fetch `API.tree(project)` → build tree
5. Auto-expand all ancestors of target task (walk `parent_id` chain, clear from `treeCollapsed`)
6. Scroll target into view, highlight selection
7. Render detail panel

**Right panel is inline (not the global `#detail-panel`)** — managed within the Alpine component as a persistent two-column layout. The global panel stays for other views. (see brainstorm: dedicated page, not slide-out)

**Progress bars on all nodes with children** — not limited to `type === "epic"`. A task decomposed into subtasks shows progress too. Calculate as `completed_children / total_children` (direct children only, not recursive). (see spec-flow gap #3)

**Tree filter preserves hierarchy** — when filtering by status/title, show matching nodes plus their ancestor chain. Non-matching siblings are hidden but the tree structure remains navigable. Client-side filter on already-fetched data. (see spec-flow gap #4)

**Refresh preserves state** — 30-second auto-refresh merges new tree data without resetting `treeCollapsed`, `selectedTaskId`, or filter text. Diff new node list against existing, update changed nodes, preserve user state. (see learnings: state reset on navigation)

### Implementation Phases

#### Phase 1: Skeleton + Nav Wiring

- [x] Create `web/views/work.js` with Alpine.js `workPage` component skeleton (loading/error/content states)
- [x] Register view: `App.registerView('work', { render, refresh })`
- [x] Update `web/index.html`: change nav link from `Projects` to `Work` (`data-view="work"`, `href="#/work"`)
- [x] Add `<script src="/views/work.js">` to index.html (before `App.init()`)
- [x] Update `web/app.js` `ROUTE_REDIRECTS`: add `projects: 'work'`, update `tasks: 'work'`, `structure: 'work'`
- [x] Modify `navigate()` in `app.js` to pass `param` (from `parseHash().param`) to `render(viewport, project, param)`
- [x] Add `.work-*` CSS foundation in `web/style.css`: two-column layout, left panel, right panel
- [x] Verify: navigating to `#/work` shows skeleton, keyboard `5` works, old `#/projects` redirects

#### Phase 2: Left Panel — Project Tree

- [x] Project picker: dropdown populated from `API.projects()`, defaults to `App.currentProject` or first
- [x] Tree rendering: fetch `API.tree(selectedProject)`, build flat tree with `buildFlatTree()` (port from `projects.js:183-225`)
- [x] Tree nodes: collapse toggle, status badge (colored dot), title, truncated ID
- [x] Progress bars on parent nodes: count completed/done children vs total children, render inline bar
- [x] Collapse/expand: `treeCollapsed` map, `isTreeVisible()` ancestor walk
- [x] Click handler: set `selectedTaskId`, trigger right panel load
- [x] Empty state: "No tasks in this project"
- [x] Escape all dynamic values with `App.escapeHtml()` (see learnings: XSS prevention)

#### Phase 3: Right Panel — Task Detail

- [x] Fetch `API.task(selectedTaskId)` on selection (returns task, dependencies, dependents, targets, decisions, traces, lessons)
- [x] Fetch `API.traces(selectedTaskId)` via `Promise.allSettled` (optional, non-blocking — see learnings: optional API resilience)
- [x] **Narrative section** (always visible): title, description (rendered via `simpleMarkdown`), acceptance criteria
- [x] **Decisions section** (always visible): list decisions with title, outcome; each shows alternatives with UCT score, visits, reward, `selected` flag
- [x] **Relationships section** (always visible): parent link (clickable → navigates tree), children count + list, dependency edges (from/to as clickable links), code targets (file:symbol)
- [x] **Execution section** (collapsible `<details>`): status badge, cost (sum from traces), estimate vs actual duration, iteration count, attempt count
- [x] **Error log section** (collapsible, auto-open when status is failed): `<pre>` block with `task.error_log`
- [x] **Traces section** (collapsible): execution trace timeline showing stage/step/tool/duration/success
- [x] Empty state when no task selected: "Select a task to view details"
- [x] Loading state between task selections: keep previous detail visible, show subtle loading indicator

#### Phase 4: Filter Bar

- [x] Text input above tree: filter nodes by title (case-insensitive substring match)
- [x] Status dropdown: filter by status (all / running / failed / completed / ready / paused)
- [x] Hierarchy-preserving filter: when a node matches, show it plus all ancestors up to root
- [x] Debounce text input (200ms)
- [x] "No results" empty state
- [x] Clear filter button (×)

#### Phase 5: Deep Links + Cross-Page Navigation

- [x] `render(viewport, project, param)`: if `param` is a task ID, trigger deep link resolution flow
- [x] Deep link flow: fetch task → resolve project → set picker → fetch tree → auto-expand ancestors → scroll into view → select → load detail
- [x] Auto-expand: walk `parent_id` chain from target task, remove each ancestor from `treeCollapsed`
- [x] Scroll into view: after tree renders, find the selected row element, call `scrollIntoView({ block: 'center' })`
- [x] `web/views/check.js`: wrap task IDs in `<a href="#/work/{fullId}" class="task-link">` (replace `<code>` with `<a>`)
- [x] `web/views/steer.js`: wrap task IDs in `<a href="#/work/{fullId}" class="task-link">` in triage card and queue items
- [x] Handle 404: if task not found, show "Task not found" in right panel, leave tree empty
- [x] Handle malformed ID: `validParam` regex check before API call

#### Phase 6: Cleanup + Polish

- [x] Remove `web/views/projects.js`
- [x] Remove `<script src="/views/projects.js">` from `index.html`
- [x] Remove `.proj-*` CSS classes from `style.css` (lines ~1677-1892)
- [x] Remove `Alpine.data('projectsPage', ...)` references
- [x] Verify 30-second auto-refresh preserves collapse/selection/filter state
- [x] Verify project switch resets tree state (collapse, selection, filter) cleanly
- [x] Verify deep links work from Check and Steer pages
- [x] Test with zero projects, empty project, single task, deep tree (50+ nodes)

## System-Wide Impact

### Interaction Graph

- `navigate()` in `app.js` calls `views['work'].render()` → fetches `API.tree()` → fetches `API.task()` on click
- Check/Steer pages gain `<a>` links that trigger `hashchange` → `navigate()` → Work page render with param
- `ROUTE_REDIRECTS` changes mean old `#/projects` URLs redirect to `#/work`
- Global `#detail-panel` is NOT used by Work page (it manages its own inline panel)
- Auto-refresh interval (30s) calls `views['work'].refresh()` → re-fetches tree, merges state

### Error Propagation

- `API.tree(project)` failure → left panel shows error, right panel shows "Select a task"
- `API.task(id)` failure → right panel shows error message, left panel unaffected
- `API.traces(id)` failure → traces section shows "unavailable" (wrapped in `Promise.allSettled`)
- Deep link with invalid task ID → 404 handled gracefully in right panel

### State Lifecycle Risks

- **Project switch must fully reset**: `treeCollapsed`, `selectedTaskId`, `filterText`, `filterStatus`, `tree` array, detail panel content
- **Refresh must preserve**: `treeCollapsed`, `selectedTaskId`, `filterText`, `filterStatus` — only update `tree` data and re-render
- **No persistent state written**: this is read-only; no risk of orphaned data

### API Surface Parity

- All APIs already exist — no backend changes
- The existing `App.openPanel(taskId)` in `app.js` continues to work for other views
- Work page does NOT call `openPanel` — it manages detail inline

## Acceptance Criteria

### Functional Requirements

- [x] Work page accessible via nav item 5 ("Work"), keyboard shortcut `5`, and `#/work`
- [x] Old `#/projects` URL redirects to `#/work`
- [x] Project picker lists all projects, defaults to current
- [x] Tree shows epic→task→subtask hierarchy with collapse/expand
- [x] Parent nodes show mini progress bars (completed children / total)
- [x] Clicking a tree node loads full detail in right panel
- [x] Detail shows: narrative (title, description, acceptance), decisions (with UCT scores + alternatives), relationships (parent, children, deps, targets)
- [x] Detail shows collapsible: execution metrics, error log, traces
- [x] Filter bar filters tree by title and/or status, preserving hierarchy
- [x] Deep link `#/work/{taskId}` auto-resolves project, expands ancestors, selects task, loads detail
- [x] Task IDs in Check feed are clickable links to `#/work/{id}`
- [x] Task IDs in Steer queue are clickable links to `#/work/{id}`
- [x] Auto-refresh (30s) preserves collapse state, selection, and filter
- [x] Project switch fully resets tree state

### Non-Functional Requirements

- [x] No new backend endpoints or database queries
- [x] All dynamic values escaped with `App.escapeHtml()` (XSS prevention)
- [x] Optional API calls wrapped in `Promise.allSettled` (resilience)
- [x] Tree renders <100ms for 200 nodes (client-side, flat array)

### Quality Gates

- [x] `projects.js` fully removed, no dead `.proj-*` CSS
- [x] All existing tests pass (`go test ./...`)
- [x] Manual verification: zero projects, empty project, deep tree, failed task deep link, project switch

## Dependencies & Risks

**Dependencies:** None — all APIs exist, all patterns established.

**Risks:**
- Large trees (500+ nodes) could be slow to filter with hierarchy preservation — mitigate with debounce and early termination
- `renderTaskDetail` logic in `app.js:243-383` is tightly coupled to the global panel — the Work page must build its own rendering (can port/adapt the existing code)

## Sources & References

### Origin

- **Brainstorm document:** [docs/brainstorms/2026-03-24-work-page-task-visibility-brainstorm.md](docs/brainstorms/2026-03-24-work-page-task-visibility-brainstorm.md) — Key decisions: master-detail layout, replaces Projects, narrative+decisions front-and-center, cross-page deep links, filter bar

### Internal References

- View pattern: `web/views/check.js` (Alpine.js component reference)
- Tree building: `web/views/projects.js:183-225` (`buildFlatTree`)
- Detail rendering: `web/app.js:243-383` (`renderTaskDetail`)
- Router: `web/app.js:190-196` (`parseHash`)
- API client: `web/app.js:12-61`
- Tree API handler: `internal/jarvis/dashboard_api.go:564` (`handleDashboardTree`)
- Task API handler: `internal/jarvis/dashboard_api.go:158` (`handleDashboardTask`)
- CSS namespacing: `web/style.css:1115` (check-*), `web/style.css:1332` (steer-*)

### Learnings Applied

- `docs/solutions/patterns/critical-patterns.md` — validate backend structs, `Promise.allSettled`, `parent_id` for hierarchy
- `docs/solutions/performance-issues/dashboard-n-plus-one-and-schema-gaps.md` — bulk-fetch patterns, avoid N+1
- `docs/solutions/ui-bugs/dashboard-pr-review-fixes-batch.md` — state reset on navigation, API contract validation
- `docs/solutions/ui-bugs/chum-dashboard-plans-tab-document-rendering.md` — tabbed layout patterns, struct field validation

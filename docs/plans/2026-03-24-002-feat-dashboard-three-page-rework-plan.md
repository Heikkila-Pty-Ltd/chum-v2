---
title: "feat: Rework dashboard into three focused pages"
type: feat
status: active
date: 2026-03-24
origin: docs/brainstorms/2026-03-12-chum-dashboard-ui-overhaul-brainstorm.md
---

# Rework Dashboard Into Three Focused Pages

## Overview

Replace the 4-tab dashboard (Overview, Structure, Jarvis KB, Plans) with 3 focused pages:

1. **Overview** — Health metrics + attention items + velocity
2. **Planner** — Agent chat interface for planning ceremonies
3. **Tasks** — Filterable task table + DAG visualization + detail panel

Kill the Jarvis KB tab entirely. The feedback loop circuit breakers just landed — the dashboard needs to surface quarantined tasks, budget-exceeded tasks, burn rate, and attempt distribution. None of this is currently visible.

## Problem Statement

The dashboard predates the feedback loop. It doesn't show:
- Health metrics (burn rate, cost/success, quarantine count)
- New terminal statuses (`quarantined`, `budget_exceeded`)
- Attempt counts or cost data on tasks
- Failure categories or attention items for the new statuses

The Jarvis KB tab is dead weight — goals/facts/initiatives are managed elsewhere. The Structure tab should merge with the task list into a single "Tasks" page.

## Proposed Solution

### Page 1: Overview (`#/overview`)

**Layout (top to bottom):**

1. **Health metrics strip** — 4-6 cards in a row:
   - Burn rate (24h): `$X.XX`
   - Cost / successful task: `$X.XX`
   - Active quarantines: `N`
   - Lessons stored: `N`
   - Tasks completed (24h): `N`
   - Tasks completed (7d): `N`

2. **Attention list** — Tasks requiring human intervention:
   - Status: `quarantined`, `budget_exceeded`, `failed`, `dod_failed`, `needs_review`, `needs_refinement`
   - Each row: status badge + task title + age + action button (retry / unquarantine)
   - Sorted by severity (quarantined first, then budget_exceeded, then failed, etc.)

3. **Per-project status bars** — One row per project showing status distribution as a stacked bar (like the existing `renderStatusBar`), with task counts

**Data sources:**
- `GET /api/dashboard/health` (NEW) — wraps `metrics.CollectHealth()`
- `GET /api/dashboard/overview-grouped/{project}` — existing, for velocity + attention tasks
- `GET /api/dashboard/projects` — existing, for multi-project iteration

**Health endpoint is global** (not project-scoped) since burn rate and quarantine count are system-level concerns. Per-project status bars use the existing per-project endpoints. The attention list iterates all projects (N calls to `overview-grouped`) so it catches quarantined tasks across the whole system, not just the selected project.

### Page 2: Planner (`#/planner`)

Keep the existing `plans.js` chat + pipeline flow largely intact:
- Sidebar: plan list for selected project
- Main area: chat interface with streaming responses
- Pipeline: groom → decompose → approve → materialize
- Task preview: tree + mini-graph after decompose

Rework scope is minimal — mostly routing/nav changes. The `cachedShell` pattern that preserves chat state across tab switches must be kept.

**Data sources:** All existing `/api/dashboard/plans/*` and `/api/dashboard/planning/*` endpoints.

### Page 3: Tasks (`#/tasks`)

**Two sub-views as tabs** (Table | DAG):

**Table view (default):**
- Columns: Status (badge) | Title | Project | Attempts | Age | Actions
- Sortable by any column (click header)
- Filterable: status multi-select dropdown, text search on title
- Click row → opens detail panel

**DAG view:**
- Dagre hierarchical layout (from existing `structure.js`)
- Hide-completed toggle
- Click node → opens detail panel
- Fingerprint-based refresh (existing pattern)

**Detail panel** (shared, slide-in from right — existing pattern):
- Task metadata: ID, title, status, type, priority, estimate, actual, attempts, cost
- Description + acceptance criteria
- Dependencies / dependents (clickable)
- Code targets
- Execution traces (NEW — collapsible per-attempt: outcome + duration + error snippet)
- Lessons learned
- Feedback buttons (for terminal tasks including quarantined/budget_exceeded)
- PR/review link
- Actions: retry, pause, kill, unquarantine (context-dependent)

**Data sources:**
- `GET /api/dashboard/tasks/{project}` — enriched with `attempt_count` and `total_cost_usd`
- `GET /api/dashboard/graph/{project}` — existing, for DAG
- `GET /api/dashboard/task/{taskID}` — existing, for detail panel
- `GET /api/dashboard/traces/{taskID}` — existing but not currently used by frontend

## Technical Approach

### Backend Changes

#### New endpoint: `GET /api/dashboard/health`

```go
// internal/jarvis/dashboard_api.go
func (a *API) handleDashboardHealth(w http.ResponseWriter, r *http.Request) {
    report, err := metrics.CollectHealth(r.Context(), a.DAG.DB(), a.TracesDB)
    if err != nil {
        a.jsonError(w, err.Error(), 500)
        return
    }
    a.jsonOK(w, report)
}
```

Requires: `TracesDB *sql.DB` field on the `API` struct (or access via store).

#### Enrich task list with attempt_count

The `attempt_count` column already exists on the tasks table. Add it to the task list response in `handleDashboardTasks`. Cost data is in the traces DB — add a batch query or accept it's not shown in the table (show in detail panel only).

#### Update attention status list

In `handleDashboardOverviewGrouped`, add `quarantined` and `budget_exceeded` to the attention query filter.

#### Update retry handler

In `handleDashboardTaskRetry`, add `quarantined` and `budget_exceeded` to the allowed-for-retry status set. For quarantined tasks, also clear the safety block and reset `attempt_count` to 0.

#### Delete Jarvis KB handlers

Remove `dashboard_jarvis.go` entirely. Remove Jarvis route registrations from `api.go`. Remove Jarvis API calls from overview.js (`jarvisSummary`, `jarvisActions`, `jarvisState`).

### Frontend Changes

#### Status model updates (`app.js`)

```javascript
// Add to STATUS_NAMES
'quarantined', 'budget_exceeded'

// Add CSS custom properties
--status-quarantined:      #8b4a8b   /* muted magenta — distinct from failed red */
--status-budget-exceeded:  #c9843a   /* amber — "limit hit" not "broken" */

// Update ATTENTION_STATUSES
['quarantined', 'budget_exceeded', 'failed', 'dod_failed', 'needs_review', 'needs_refinement']

// Update FAILED_STATUSES
['quarantined', 'budget_exceeded', 'failed', 'dod_failed', 'rejected']

// Terminal statuses (for feedback buttons)
['completed', 'done', 'failed', 'dod_failed', 'quarantined', 'budget_exceeded']
```

#### Navigation updates (`index.html`, `app.js`)

3 nav links: Overview | Planner | Tasks

Keyboard shortcuts: 1=overview, 2=planner, 3=tasks

Route redirects: `#/structure` → `#/tasks`, `#/jarvis` → `#/overview`

#### File changes

| File | Action | Notes |
|------|--------|-------|
| `web/index.html` | Edit | 3 nav links, remove jarvis script tag |
| `web/app.js` | Edit | Status model, routing, detail panel traces, keyboard shortcuts |
| `web/views/overview.js` | Rewrite | Health strip + attention list + per-project bars |
| `web/views/plans.js` | Edit | Re-register as 'planner' view, keep filename |
| `web/views/structure.js` | Rewrite → `tasks.js` | Table + DAG tabs + filters |
| `web/views/jarvis.js` | Delete | Killed |
| `web/style.css` | Edit | New status colors, health strip styles, table styles |
| `web/style_plans.css` | Keep | Rename import if plans.js renamed |
| `internal/jarvis/api.go` | Edit | Add health route, remove jarvis routes |
| `internal/jarvis/dashboard_api.go` | Edit | Add health handler, enrich task list, update attention/retry |
| `internal/jarvis/dashboard_jarvis.go` | Delete | Killed |

### Implementation Phases

#### Phase 1: Backend + Status Model

- [x] Add `TracesDB` access to API struct (for health endpoint)
- [x] Create `handleDashboardHealth` endpoint
- [x] Add `attempt_count` to task list response
- [x] Add `quarantined`/`budget_exceeded` to attention status filter
- [x] Add `quarantined`/`budget_exceeded` to retry handler (with safety block clear)
- [x] Delete `dashboard_jarvis.go` and remove Jarvis routes from `api.go`
- [x] Add new status CSS custom properties to `style.css`
- [x] Update `STATUS_NAMES`, `ATTENTION_STATUSES`, `FAILED_STATUSES` in `app.js`
- [x] Update terminal status check in `renderTaskDetail`

#### Phase 2: Navigation Shell

- [x] Update `index.html`: 3 nav links, remove jarvis.js script tag
- [x] Update `app.js` routing: 3 views, redirects for old routes
- [x] Update keyboard shortcuts (1/2/3)
- [x] Delete `web/views/jarvis.js`

#### Phase 3: Overview Page

- [x] Rewrite `overview.js` with health metrics strip
- [x] Add attention list with action buttons
- [x] Add per-project status bars
- [x] Style health cards, attention rows
- [x] Empty states: "No attention items" when clean, "$0.00" when traces DB is fresh
- [x] Auto-refresh on 30s interval

#### Phase 4: Tasks Page

- [x] Create `tasks.js` with table + DAG tab toggle
- [x] Task table: sortable columns, status filter dropdown, text search
- [x] DAG view: dagre layout from existing `structure.js` patterns
- [x] Wire detail panel clicks from both table rows and DAG nodes
- [x] Add execution traces rendering to detail panel
- [x] Preserve filter/sort state across refresh
- [ ] Fingerprint-based DAG refresh

#### Phase 5: Planner + Cleanup

- [x] Re-register `plans.js` as 'planner' view (change `App.registerView` call, keep filename)
- [x] Remove Jarvis API calls from overview data loading
- [x] Verify `cachedShell` pattern survives view switches
- [x] Test streaming chat, pipeline actions, task preview
- [x] Remove dead CSS (ov-jarvis-*, jarvis-* selectors)
- [x] Remove unused API client methods (jarvisGoals, jarvisFacts, etc.)
- [x] Verify `dashboard-preview` command still works
- [x] Test all three pages with empty database
- [ ] Test with populated database

## Key Decisions

1. **Health endpoint is global, not per-project** — burn rate and quarantine count are system-level. Per-project drill-down is via the existing project endpoints.

2. **Table and DAG are tabs, not side-by-side** — simpler layout, avoids cramming table + graph + detail panel into one viewport.

3. **Cost not in task table** — it requires cross-DB query (traces). Show it in the detail panel only (already available via traces endpoint). Attempt count IS in the table (same DB).

4. **"Stuck in review" = any task with status `needs_review`** — no time-based threshold. Keep it simple, add duration-based logic later if needed.

5. **Jarvis goals/facts/initiatives are dropped** — not moved to overview. The API endpoints stay for other consumers (Matrix bot). Only the frontend references and Jarvis-specific handler file are deleted.

6. **Old routes redirect** — `#/structure` → `#/tasks`, `#/jarvis` → `#/overview`.

7. **Quarantined tasks are retryable** — retry clears the safety block and resets attempt_count. Budget-exceeded is also retryable (re-dispatches with same budget, user can adjust config).

## Acceptance Criteria

- [ ] `#/overview` shows health metrics strip with burn rate, cost/success, quarantine count, lesson count, velocity
- [ ] `#/overview` shows attention list with quarantined, budget_exceeded, failed tasks + action buttons
- [ ] `#/overview` shows per-project status distribution bars
- [ ] `#/planner` provides the full planning chat + pipeline flow
- [ ] `#/tasks` shows filterable/sortable task table with status, title, project, attempts, age (cost in detail panel only)
- [ ] `#/tasks` shows dagre DAG visualization as alternate tab
- [ ] Task detail panel shows execution traces, lessons, feedback, PR links
- [ ] `quarantined` and `budget_exceeded` statuses have distinct colors and appear correctly everywhere
- [ ] Retry works for quarantined tasks (clears safety block + resets attempts)
- [ ] Jarvis KB tab fully removed (frontend + backend handler file)
- [ ] Old routes (`#/structure`, `#/jarvis`) redirect to new pages
- [ ] Keyboard shortcuts 1/2/3 map to the three pages
- [ ] All pages handle empty state gracefully
- [ ] Auto-refresh (30s) works on all pages without losing user state
- [ ] `go build ./...` and `go test ./...` pass
- [ ] `dashboard-preview` command works

## Dependencies

- **Circuit breaker work** (just landed) — `quarantined` and `budget_exceeded` statuses, `attempt_count` column, `metrics.CollectHealth()` function
- **Existing dashboard** — keeping the SPA shell, API handler patterns, detail panel, CSS design tokens

## Sources & References

- **Origin brainstorm:** [docs/brainstorms/2026-03-12-chum-dashboard-ui-overhaul-brainstorm.md](docs/brainstorms/2026-03-12-chum-dashboard-ui-overhaul-brainstorm.md) — partially superseded (kept 4 tabs including Jarvis, predates circuit breakers)
- **Institutional learning:** [docs/solutions/ui-bugs/chum-dashboard-plans-tab-document-rendering.md](docs/solutions/ui-bugs/chum-dashboard-plans-tab-document-rendering.md) — validate frontend against actual Go structs before building UI
- **Status constants:** `internal/types/types.go:22-50`
- **SPA router:** `web/app.js:198-229`
- **API registration:** `internal/jarvis/api.go:37-106`
- **Health metrics:** `internal/metrics/health.go`
- **CSS tokens:** `web/style.css:1-63`

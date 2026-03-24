---
title: "feat: Dashboard Five-Jobs Rethink"
type: feat
status: active
date: 2026-03-24
deepened: 2026-03-24
origin: docs/brainstorms/2026-03-24-dashboard-rethink-five-jobs-brainstorm.md
---

# Dashboard Five-Jobs Rethink

## Enhancement Summary

**Deepened on:** 2026-03-24
**Research agents used:** Alpine.js patterns, dashboard UX, Go API endpoints, SVG sparklines, drag-drop, architecture-strategist, security-sentinel, performance-oracle, code-simplicity-reviewer, learnings-researcher (10 docs)

### Key Improvements
1. **Simplified scope** — removed comparison view, Plan Review mode, sortable model table, fuzzy antipattern grouping. ~350 LOC avoided.
2. **Phase reordering** — Steer (highest operator value) moved to Phase 2, before Projects and Learn.
3. **Alpine.js lifecycle adapter** — concrete `render/refresh/destroy` contract defined for integration with existing SPA shell.
4. **Self-hosted Alpine.js** — pinned version in `web/vendor/`, eliminates CDN failure mode.
5. **Security hardening** — bind to localhost + SSH tunnel, input validation, rate limiting on `/suggest`.
6. **Performance fixes** — missing indexes on `perf_runs.created_at` and `task_edges.to_task`, bulk-fetch to eliminate N+1 queries in tree/overview handlers, ETag/304 for polling.
7. **Schema fix** — `perf_runs.task_id` column referenced in `metrics/health.go` but missing from `perf/schema.go`.

### New Considerations Discovered
- Alpine.js `init()/destroy()` lifecycle must be manually wired into the existing `App.registerView()` contract via `Alpine.initTree()/destroyTree()` on navigation
- The existing `handleDashboardTree` has O(N) queries per task (deps, decisions, traces) — must be bulk-fetched before building the Projects page
- `perf_runs` table is missing a `task_id` column and a `created_at` index — both needed by new endpoints
- Up/down arrow buttons replace drag-to-reorder for the single-operator use case (~80% less code)

---

## Overview

Replace the current 3-page dashboard (Overview, Planner, Tasks) with 5 focused pages, each built around a specific user job: Check, Plan, Steer, Learn, Projects. The backend already has 13+ API endpoints returning rich data — grouped goals with velocity, lessons with FTS, execution traces with phase breakdown, UCT decisions, triage suggestions — that the frontend doesn't surface. Several write endpoints also exist (pause, kill, retry, decompose, planning signals). This is primarily a frontend rethink with targeted backend additions.

(see brainstorm: docs/brainstorms/2026-03-24-dashboard-rethink-five-jobs-brainstorm.md)

## Problem Statement

The dashboard is a black box. CHUM's self-healing loop, learning history, cost model, and decision intelligence are invisible. The user can't answer basic questions: "What happened overnight?", "Is the system improving?", "Which project needs attention?", "Should I redirect effort?" The current Overview dumps raw health numbers, Tasks shows a flat list of 386 items, and the Planner chat has no visibility into plan structure.

## Proposed Solution

Five job-per-page views sharing the existing SPA shell:

| Page | Job | Primary Mode |
|------|-----|--------------|
| `#/check` | What happened since I last looked? | Read — summary + timeline |
| `#/steer` | Reprioritize, pause, redirect | Write — triage, allocate, replan |
| `#/projects` | Per-project detail + portfolio | Read — health cards, drill-in |
| `#/learn` | Is the system improving? | Read — trends, lessons, model perf |
| `#/plan` | Create and manage plans | Write — tree view + agent chat |

## Technical Approach

### Architecture

The SPA shell (`web/app.js`) stays intact. Each page becomes a new view file in `web/views/` using **Alpine.js** for declarative reactivity (15KB, no build step, self-hosted in `web/vendor/`). Views register via `Alpine.data('pageName', () => ({...}))` inside an `alpine:init` event listener. The API client (`App.API`) gets new method shortcuts per phase. CSS extends the existing token system (`:root` variables in `style.css`).

#### Alpine.js Lifecycle Adapter

Alpine.js must integrate with the existing `App.registerView(name, { render, refresh })` contract. Convention for all new views:

```javascript
// render(viewport, project): Insert Alpine-attributed HTML, then init
render(viewport, project) {
    viewport.innerHTML = `<div x-data="checkPage" x-ref="root">...</div>`;
    Alpine.initTree(viewport);
},
// refresh(project): Update Alpine data without replacing DOM
refresh(project) {
    const el = document.querySelector('[x-data="checkPage"]');
    if (el && el._x_dataStack) el._x_dataStack[0].refresh();
},
// App.navigate() must call Alpine.destroyTree(viewport) before clearing innerHTML
// This prevents leaked intervals, watchers, and event listeners
```

Each Alpine component uses `init()` for first load + auto-refresh setup, and `destroy()` for cleanup:

```javascript
Alpine.data('checkPage', () => ({
    timer: null,
    async init() { await this.refresh(); this.timer = setInterval(() => this.refresh(), 30000); },
    destroy() { clearInterval(this.timer); },
    async refresh() { /* fetch data, update reactive properties */ }
}));
```

#### Alpine.js Self-Hosting

Download pinned Alpine.js v3 into `web/vendor/alpine.min.js` instead of CDN. Load via `<script defer src="vendor/alpine.min.js">`. Optional plugins (Sort, Collapse) also self-hosted if used. This eliminates external dependency failure mode.

**Script load order**: Component registrations (`alpine:init` event listeners) must be loaded BEFORE Alpine core due to `defer` execution order. Register all `Alpine.data()` calls inside `document.addEventListener('alpine:init', ...)` which is order-independent.

**Files touched:**
- `web/index.html` — nav links (5 items instead of 3), Alpine.js script tag
- `web/vendor/alpine.min.js` — NEW (self-hosted Alpine.js v3, pinned version)
- `web/app.js` — route redirects, keyboard shortcuts (1-5), Alpine.destroyTree in navigate(), add API methods per phase (not all at once)
- `web/style.css` — new page-specific styles appended
- `web/views/check.js` — NEW
- `web/views/steer.js` — NEW
- `web/views/projects.js` — NEW
- `web/views/learn.js` — NEW
- `web/views/plan.js` — REWRITE of plans.js (keep planner session logic)
- `web/views/overview.js` — DELETE (replaced by check)
- `web/views/tasks.js` — DELETE (absorbed into projects detail + steer)
- `internal/jarvis/dashboard_api.go` — new endpoints
- `internal/jarvis/api.go` — register new routes
- `internal/perf/schema.go` — add `task_id` column + indexes

### Existing Endpoints (No Backend Work)

These endpoints already return data needed by the new pages:

| Endpoint | Used By | Notes |
|----------|---------|-------|
| `GET /overview/{project}` | Check | running, recent 24h, attention lists |
| `GET /overview-grouped/{project}` | Projects, Check | goal groups with velocity + health |
| `GET /timeline/{project}` | Check | chronological task feed |
| `GET /tasks/{project}` | Steer | filterable task list |
| `GET /task/{taskID}` | All (detail panel) | full task + deps + traces + lessons |
| `GET /tree/{project}` | Plan, Projects | decomposition hierarchy |
| `GET /stats/{project}` | Projects | status counts, time metrics |
| `GET /health` | Check, Learn | burn rate, cost/success, quarantines |
| `GET /lessons/{project}` | Learn | recent lessons, FTS searchable |
| `GET /suggest/{taskID}` | Steer | LLM triage suggestion (cached) |
| `GET /traces/{taskID}` | Learn, Steer | execution traces + graph events |
| `GET /plans/{project}` | Plan | plan list |
| `GET /plan/{id}` | Plan | plan detail |
| `POST /task/{id}/pause` | Steer | pause running task |
| `POST /task/{id}/kill` | Steer | terminate task |
| `POST /task/{id}/retry` | Steer | reset failed task |
| `POST /task/{id}/decompose` | Steer | mark for re-decomposition |
| `POST /planning/start` | Plan | start planning workflow |
| `POST /planning/{id}/signal` | Plan | signal planning workflow |
| `POST /plan/{id}/groom` | Plan | chat/groom (streaming) |
| `POST /plan/{id}/decompose` | Plan | decompose plan into tasks |
| `POST /plan/{id}/approve` | Plan | approve plan |
| `POST /plan/{id}/materialize` | Plan | materialize to DAG |
| `POST /system/pause` | Steer | system-wide pause |
| `POST /system/resume` | Steer | system-wide resume |

### New Backend Endpoints Required

All under `/api/dashboard/` prefix to match existing convention:

| Endpoint | Page | Purpose |
|----------|------|---------|
| `GET /api/dashboard/activity` | Check | Cross-project activity feed from execution_traces + lessons. Queries both DBs, merges in Go. |
| `GET /api/dashboard/learning/trends` | Learn | Aggregated daily/weekly: success rate, cost per task, attempt average. From perf_runs. |
| `GET /api/dashboard/learning/model-perf` | Learn | Per-model: task count, success rate, avg cost, avg duration. From perf_runs. |
| `POST /api/dashboard/project/{name}/pause` | Steer | Pause all ready/running tasks in project. Return count affected. |
| `POST /api/dashboard/queue/reorder` | Steer | Reorder task execution priority. Body: `{task_ids: [...]}` in desired order. |

**Deferred (not in initial build):**
- `POST /project/{name}/replan` — stop tasks + re-enter planning. Complex; involves Temporal workflow cancellation. Build the UI with a "coming soon" placeholder.
- `POST /project/{name}/allocate` — set concurrency ceiling per project. Requires dispatcher changes. Defer to Phase 5+.

### Pre-Implementation: Schema & Performance Fixes

Before building any new endpoints, fix these discovered issues:

- [x] **Add `task_id` column to `perf_runs`** — referenced in `metrics/health.go:103` but missing from `perf/schema.go`. Add via `ALTER TABLE perf_runs ADD COLUMN task_id TEXT DEFAULT ''`.
- [x] **Add index on `perf_runs.created_at`** — currently full table scan on every health/trends query. `CREATE INDEX IF NOT EXISTS idx_perf_runs_created ON perf_runs(created_at)`.
- [x] **Add index on `perf_runs.task_id`** — `CREATE INDEX IF NOT EXISTS idx_perf_runs_task ON perf_runs(task_id)`.
- [x] **Add index on `task_edges.to_task`** — `GetDependents` queries on `to_task` alone, which can't use the composite PK `(from_task, to_task)`. `CREATE INDEX IF NOT EXISTS idx_task_edges_to ON task_edges(to_task)`.
- [x] **Add index on `tasks.project`** — every `ListTasks` filters by project. `CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project, priority, created_at)`.
- [x] **Bulk-fetch refactor in `handleDashboardTree`** — replace per-task `GetDependencies`/`GetDependents`/`ListDecisionsForTask`/`ListExecutionTraces` with bulk queries + Go-side lookup maps. Collapses O(N) queries to O(1).
- [x] **Bulk-fetch refactor in `handleDashboardOverviewGrouped`** — same pattern for the `toChild` function's per-task `GetDependencies` calls.
- [x] **Replace trace existence check** — `dashboard_api.go:614-618` fetches all traces just to check `len > 0`. Use `SELECT DISTINCT task_id FROM execution_traces WHERE task_id IN (...)` bulk query instead.
- [x] **Add TTL to `suggestCache`** — currently unbounded in-memory cache. Add 10-minute TTL so stale suggestions after retries get refreshed.
- [x] **Bind HTTP server to `127.0.0.1`** — access via SSH tunnel (`ssh -L 9781:127.0.0.1:9781`). Eliminates auth/TLS/CSRF concerns with zero code changes.
- [x] **Add rate limiting on `/suggest/{taskID}`** — 2-second cooldown between LLM calls to prevent cost bomb.
- [x] **Input validation** — `validParam()` checks path parameters against `[a-zA-Z0-9._:-]` allowlist; applied to tree, overview-grouped, and suggest handlers.

### Implementation Phases

**Phase order rationale:** Steer (write-heavy action surface) comes right after Check because it matches the operator's daily workflow: check what happened, then take action. Projects and Learn are read-only and lower urgency. Plan rework is riskiest (35K-line plans.js rewrite) and goes last.

#### Phase 1: Foundation + Check (`web/views/check.js`)

The morning check page and the shell changes that all subsequent phases depend on.

**Shell changes (`web/app.js`, `web/index.html`):**
- [x] Add Alpine.js: download v3 to `web/vendor/alpine.min.js`, add script tag to `index.html`
- [x] Add `Alpine.destroyTree(viewport)` call in `App.navigate()` before clearing innerHTML
- [x] Update nav in index.html: 5 links (Check, Plan, Steer, Learn, Projects)
- [x] Update route redirects: `{ overview: 'check', structure: 'projects', jarvis: 'check', plans: 'plan', tasks: 'projects' }`
- [x] Update keyboard shortcuts: 1=check, 2=plan, 3=steer, 4=learn, 5=projects
- [x] Add API method: `activity()` (only methods needed for this phase)
- [x] Default route → `#/check`

**Backend (`internal/jarvis/dashboard_api.go`):**
- [x] `GET /api/dashboard/activity` endpoint
  - Query `execution_traces` from chum-traces.db: recent traces with outcome, cost, timestamps
  - Query `lessons` from chum-traces.db: recent lessons with task_id, timestamp
  - Merge in Go, sort reverse-chronological (do NOT use ATTACH DATABASE — follow existing `CollectHealth()` pattern of separate queries + Go merge)
  - Default last 24h (`?hours=N` param, parsed to int, bounded 1-8760)
  - Each event: `{type: "trace"|"lesson", task_id, title, project, status, outcome, timestamp, cost_usd, summary}`
  - Register route in `api.go`

**Frontend (`web/views/check.js`):**
- [x] Register via `Alpine.data('checkPage', () => ({...}))` inside `alpine:init` listener
- [x] Summary strip at top (5 KPI cards, horizontal row):
  - Needs Attention count (from `/overview/{project}` attention list) — links to `#/steer`
  - Completed 24h count + total cost (from `/activity`)
  - In Progress count (from `/activity`)
  - Cost 24h (from `/activity`)
  - Success rate (from `/health`)
  - Use `Promise.allSettled` — health/activity are required; overview per-project is optional enrichment
- [x] Timeline feed below:
  - Fetch `/activity` (cross-project)
  - Each row: relative time, task ID (mono), title, project tag, outcome badge (status color+label), cost chip
  - Failed items expand to show error log (progressive disclosure via `x-show`)
  - Filter bar between strip and timeline: project dropdown + severity (all/attention/completed/lessons) via `x-model`
  - Use `x-text` everywhere — never `x-html` for user data
- [x] `refresh()`: re-fetch activity + counts, merge into Alpine reactive data (do NOT replace DOM)
- [x] Surface circuit breaker state: quarantined count (from health endpoint, shown when > 0)

**CSS:** New `.check-*` classes in `style.css` — done.

**Note:** Keep `web/views/overview.js` and `tasks.js` alive with route redirects until all phases are complete. Delete them only after Phase 5 when all replacements are live.

#### Phase 2: Steer (`web/views/steer.js`)

The write-heavy control surface. Highest operator value — this is where action happens.

**Backend:**
- [x] `POST /api/dashboard/project/{name}/pause` endpoint
  - Validate project name: `[a-zA-Z0-9_-]`, max 128 chars
  - Use `BeginTx`, batch `UPDATE tasks SET status = 'paused', updated_at = datetime('now') WHERE project = ? AND status IN ('running', 'ready')`
  - Return `{project, affected, new_status}` — re-calling is idempotent (returns `affected: 0`)
  - Call `TriggerDispatch` after update (same pattern as `handleDashboardTaskRetry`)
  - Register route
- [x] `POST /api/dashboard/queue/reorder` endpoint
  - Accept `{task_ids: ["id1", "id2", ...]}`, validate: max 1000, no duplicates, all must exist and be `ready` status
  - `BeginTx`, loop update `priority = position_index` for each ID
  - Call `TriggerDispatch` after commit
  - Register route
- [x] Add API methods: `projectPause(name)`, `queueReorder(ids)`

**Frontend — three sections:**

- [x] Triage panel:
  - Fetch attention tasks from `/overview/{project}`
  - For each failed/stuck task: ID, title, failure reason (from error_log), age (relative time)
  - "Triage" button → calls `/suggest/{taskID}` → shows agent suggestion inline
  - Suggestion rendered as provisional: dashed border, "Suggested" label, reduced opacity
  - Action buttons: Accept (retry/skip/decompose via existing endpoints), Dismiss, Override
  - Keyboard shortcuts for triage flow: `j/k` navigate, `a` accept, `d` dismiss
  - Triage queue empties as items are resolved

- [x] Execution queue:
  - Fetch `/tasks/{project}?status=ready` + running tasks from `/overview/{project}`
  - Running tasks at top (read-only, pulsing indicator)
  - Ready tasks below in priority order
  - **Up/down arrow buttons** per ready task to reorder (simpler than drag-drop for single operator, ~80% less code)
  - On reorder: optimistic UI update, POST `/queue/reorder` with new ID order, rollback on failure
  - Per-project grouping with collapse

- [x] Project controls:
  - Per-project row: name, running count, queued count, pause/resume toggle
  - Pause → `POST /project/{name}/pause`
  - Resume → `POST /project/{name}/resume` (per-project resume built)
  - "Replan" button → placeholder with tooltip "Coming soon"

#### Phase 3: Projects (`web/views/projects.js`)

Portfolio view + per-project drill-in.

**Frontend:**
- [x] Project list view (default):
  - Fetch `/projects` + `/stats/{project}` + `/overview-grouped/{project}` for each
  - Use `Promise.allSettled` — secondary project data is optional enrichment
  - Each project renders a health card (consistent anatomy):
    - Name, % complete (completed+done / total), velocity (from overview-grouped 24h/7d)
    - Status bar (colored segments by status count)
    - Burn rate, blocked count, cost summary
    - Health indicator (shape+color+label: healthy/degraded/failing) — unhealthy projects sort first by default
  - Cards in responsive grid, compact enough for 3-5 to fit in a single row
  - ~~Comparison: side-by-side layout when 2+ projects selected~~ **REMOVED** — with 3-5 projects the card grid IS the comparison view

- [x] Project detail view (click into card):
  - Route: `#/projects/{name}`
  - Task tree from `/tree/{project}` — flat-tree approach (items with `depth` field, single Alpine reactive context) rather than recursive nested `x-data` for performance
  - Status rollup per tree node. Include `cancelled`, `stale`, `quarantined`, `budget_exceeded` with distinct visual treatment
  - Use `parent_id` for hierarchy (goal → subtask tree). Use edges only for dependency ordering. Reference `internal/dag/edges.go:L9` convention
  - Links section: GH repo URL (from config), active PRs, deployment status
  - Cost history (from perf_runs aggregated by day)
  - Active safety blocks (from health data, filtered to project)
  - Plan list (from `/plans/{project}`)
  - Reset all filter/search state on project switch

- [x] Add API method: `overviewGrouped(project)` if not already present

**Note:** DAG visualization from tasks.js is deferred — not rebuilt in this phase. Keep tasks.js alive until Phase 5 cleanup.

#### Phase 4: Learn (`web/views/learn.js`)

System improvement observatory.

**Backend:**
- [x] `GET /api/dashboard/learning/trends` endpoint
  - `SELECT date(created_at) as day, COUNT(*) as total, SUM(success) as successes, CAST(SUM(success) AS REAL)/COUNT(*) as success_rate, AVG(duration_s) as avg_duration, SUM(cost_usd) as total_cost FROM perf_runs WHERE created_at >= date('now', '-30 days') GROUP BY date(created_at) ORDER BY day ASC`
  - Gap-fill in Go: days with no runs appear with zero values (sparklines need consistent x-axis spacing)
  - Response: `{trends: [{day, total_runs, successes, success_rate, avg_duration_s, total_cost_usd}], period_days: 30}`
  - Register route
- [x] `GET /api/dashboard/learning/model-perf` endpoint
  - Reuse pattern from `perf.StatsForTier` but without tier filter
  - `SELECT agent, model, tier, COUNT(*), SUM(success), ... FROM perf_runs GROUP BY provider_key ORDER BY total_runs DESC`
  - Response: `{models: [{agent, model, tier, total_runs, success_rate, avg_cost_usd, avg_duration_s}]}`
  - Register route
- [x] Add API methods: `learningTrends()`, `modelPerf()`

**Frontend:**
- [x] Headline metrics row (3-4 metric cards with sparklines):
  - Success rate sparkline (30 days) — inline SVG polyline, `viewBox="0 0 100 30"`, `preserveAspectRatio="none"`
  - Cost per successful task sparkline
  - Average attempts sparkline
  - Each shows: large current value, small label, sparkline (color-coded by trend), trend arrow + percentage
  - Sparkline component: normalize data to viewBox coords, `y = pad + (H - 2*pad) * (1 - (val - min) / range)`, area fill via polygon + linear gradient
  - Color: `higherIsBetter` flag per metric — success rate up = green, cost up = red
  - Accessibility: `role="img"`, `aria-label` describing trend direction and values
  - Pre-compute points string in Alpine getter (not per-frame)
  - Use unique gradient IDs per sparkline (`'sg-' + Math.random().toString(36).slice(2,8)`)

- [x] Lesson feed:
  - Fetch `/lessons/{project}` (or all projects)
  - Each lesson: category badge (color+text), summary, associated task link, timestamp (relative)
  - Group by exact `(category, summary)` pair — show count badge "x5" for repeated identical lessons. ~~Fuzzy text matching~~ **REMOVED** — exact dedup catches real repeat offenders without undefined heuristics
  - Search box using FTS (`?q=` param) with `x-model.debounce.300ms`
  - Show pattern vs antipattern breakdown
  - Surface whether lessons are being consumed by agent executions (not just stored)

- [x] Model performance table:
  - Fetch `/learning/model-perf`
  - Columns: model, tasks, success%, avg cost, avg duration
  - Pre-sorted by task_count desc from backend. ~~Client-side sortable columns~~ **REMOVED** — with 3-5 models, scanning the table is faster than clicking sort headers
  - Color-code success rate (green >80%, yellow >60%, red <60%) — both color and text label

#### Phase 5: Plan Rework (`web/views/plan.js`)

Rewrite existing plans.js. Create mode only — ~~review mode~~ **REMOVED** (queue review is covered by Check summary + Steer detailed queue).

**Frontend — Create mode (the Plan page):**
- [x] Plan list sidebar (from existing plans.js):
  - Fetch `/plans/{project}`
  - Plan cards with status badge
  - "New Plan" button

- [x] Structured plan view (left panel, 60-65% width):
  - Existing plans.js already renders `brief_markdown`, `working_markdown`, `structured` fields
  - Tasks grouped by `batch` field in task preview table
  - Dependency graph view with dagre layout
  - Pipeline actions: decompose/approve/materialize buttons per plan status
  - Task detail view with description, acceptance criteria, estimates

- [x] Agent chat (right panel, 35-40% width):
  - Preserved existing planner session logic from plans.js (SSE streaming, chat bubbles, tool use)
  - Registered view as `plan` instead of `planner` to match new nav route
  - Transport contract: JSON for plan data, SSE for chat streaming (already implemented)

**Carry forward from plans.js:** Session management, SSE handling, chat rendering. Refactor into the single-mode structure but don't rewrite the planner session protocol.

## Acceptance Criteria

### Functional

- [ ] Five nav items: Check, Plan, Steer, Learn, Projects (keyboard 1-5)
- [ ] Check page: summary strip (4-5 KPI cards) + activity timeline, filterable by project
- [ ] Check page: attention items link to Steer for triage
- [ ] Check page: circuit breaker state visible (quarantined, budget_exceeded, retry limit)
- [ ] Projects page: health cards for all projects, unhealthy first
- [ ] Projects page: drill into project detail (flat tree, stats, links, cost, safety blocks)
- [ ] Learn page: success/cost/attempt sparklines (30 days) with trend arrows
- [ ] Learn page: lesson feed with exact-match dedup grouping
- [ ] Learn page: model performance table (pre-sorted)
- [ ] Steer page: agent-assisted triage with suggestion + action buttons
- [ ] Steer page: execution queue with up/down reorder buttons
- [ ] Steer page: per-project pause/resume
- [ ] Plan page: plan list + structured tree view + agent chat

### Non-Functional

- [ ] Alpine.js self-hosted in `web/vendor/` (pinned v3, no CDN dependency)
- [ ] All dynamic content rendered via `x-text` — no `x-html` with user data (XSS prevention)
- [ ] Uses existing CSS token system (no new color definitions outside `:root`)
- [ ] All API calls use `Promise.allSettled` for optional/secondary fetches
- [ ] Validate frontend against actual Go struct fields before building UI — read the Go struct, use only fields that exist
- [ ] 30s auto-refresh via Alpine `init()/destroy()` lifecycle (merge data, don't replace DOM)
- [ ] HTTP server bound to `127.0.0.1` — access via SSH tunnel
- [ ] Status indicators use shape + color + text label (never color alone — accessibility)
- [ ] All new endpoints under `/api/dashboard/` prefix

### Quality Gates

- [ ] All existing Go tests pass
- [ ] New endpoints have tests (at minimum: activity, learning/trends, model-perf, project pause, queue reorder)
- [ ] `gofmt -w` on all changed Go files
- [ ] No console errors on page load for any view
- [ ] Missing indexes added (perf_runs.created_at, perf_runs.task_id, task_edges.to_task, tasks.project)
- [ ] N+1 queries eliminated in tree and overview-grouped handlers

## Dependencies & Risks

**Dependencies:**
- Alpine.js v3 (self-hosted in `web/vendor/alpine.min.js`)
- Existing planner session infrastructure (tmux-backed) for Plan create mode
- Both databases (chum.db + chum-traces.db) must be connected for activity/learning endpoints — every new endpoint must explicitly declare which DB(s) it needs

**Risks:**
- **Alpine.js lifecycle collision with existing SPA shell (MEDIUM).** The existing `App.navigate()` wipes `viewport.innerHTML` which destroys Alpine components. Mitigation: Add `Alpine.destroyTree(viewport)` before clearing, and `Alpine.initTree(viewport)` after rendering. Phase 1 validates this integration before 4 more views are built.
- **plans.js is 35K lines (HIGH).** Rewriting risks breaking planner chat. Mitigation: Phase 5 preserves session protocol logic, refactors around it. If Alpine integration proves awkward in Phase 1, we can stay vanilla for Plan page.
- **`perf_runs.task_id` schema gap (LOW).** Column is referenced in health.go but missing from schema. Fix in pre-implementation phase with `ALTER TABLE`.
- **Cross-project activity endpoint latency (LOW).** Queries both DBs, merges in Go. Mitigation: Default 24h window, cursor-based pagination if needed, indexes on `created_at`.
- **No status change log (KNOWN LIMITATION).** Activity feed built from execution_traces + lessons, not status transitions. Completed tasks only appear if they have a trace. Acceptable — most tasks do.

## Sources & References

### Origin

- **Brainstorm:** [docs/brainstorms/2026-03-24-dashboard-rethink-five-jobs-brainstorm.md](docs/brainstorms/2026-03-24-dashboard-rethink-five-jobs-brainstorm.md)
  - Key decisions: 5 job-per-page layout, Check read/Steer write separation, allocation-based priority, agent-assisted triage, two-mode planning, layered learning

### Internal References

- SPA shell: `web/app.js:141-175` (view registration + routing)
- API endpoints: `internal/jarvis/dashboard_api.go` (1205 lines, all endpoints)
- Route registration: `internal/jarvis/api.go:65-94`
- CSS tokens: `web/style.css:9-66` (:root variables)
- Existing overview-grouped with velocity: `dashboard_api.go:768-961`
- Existing suggest/triage: `dashboard_api.go:1114-1172`
- Health metrics: `internal/metrics/health.go` (CollectHealth with two-DB pattern)
- Perf schema: `internal/perf/schema.go` (missing task_id column)
- DAG edge convention: `internal/dag/edges.go:L9` (from=dependent, to=prerequisite)
- Critical patterns: `docs/solutions/patterns/critical-patterns.md`

### Documented Learnings (Applied)

- **Transport contract validation** (`docs/solutions/ui-bugs/dashboard-pr-review-fixes-batch.md`) — nail down JSON vs SSE per endpoint before writing fetch calls
- **Two-DB threading** (`docs/solutions/integration-issues/dashboard-preview-missing-traces-db-flag.md`) — every handler must receive both `*sql.DB` connections explicitly
- **Frontend/backend contract** (`docs/solutions/ui-bugs/chum-dashboard-plans-tab-document-rendering.md`) — PlanDoc has `batch` grouping not hierarchical `type`, render `brief_markdown`/`working_markdown`/`structured` fields
- **Stale filter state** (`docs/solutions/ui-bugs/dashboard-three-page-rework.md`) — reset all filter/search state on project switch
- **Task lifecycle statuses** (`docs/solutions/pipeline-issues/task-accumulation-orphans-dupes-stale.md`) — `cancelled`, `stale` are real statuses needing UI treatment
- **Feedback loop visibility** (`docs/solutions/logic-errors/feedback-loop-never-acts-on-outcomes.md`) — surface circuit breakers, burn rate, whether lessons are consumed
- **Progressive disclosure for LLM context** (`docs/solutions/integration-issues/planner-codebase-context-injection.md`) — use `FormatForPrompt`, reuse `context_snapshot` caching
- **Decomposition quality** (`docs/solutions/pipeline-issues/decomposition-tangent-scope-drift.md`) — surface enforcement metrics on Steer page

### Research (from /deepen-plan)

- Alpine.js v3 patterns: `init()/destroy()` lifecycle, `Alpine.initTree()/destroyTree()`, `Alpine.data()` registration, `x-model.debounce`, flat-tree for performance
- Dashboard UX: KPI summary strip + timeline (Grafana/Datadog pattern), progressive disclosure, severity via shape+color+label, keyboard-first triage (Linear pattern)
- Go API: separate-query-merge-in-Go for cross-DB (not ATTACH), cursor pagination for activity, `BeginTx` for batch updates, `TriggerDispatch` after write endpoints
- SVG sparklines: `viewBox="0 0 100 30"` + `preserveAspectRatio="none"`, area fill via polygon + gradient, `currentColor` inheritance, unique gradient IDs, pre-compute points
- Security: bind to localhost + SSH tunnel (highest leverage), rate limiting on `/suggest`, input validation on all user params, `x-text` not `x-html`
- Performance: indexes on `perf_runs.created_at`/`task_id`, `task_edges.to_task`, `tasks.project`; bulk-fetch to eliminate N+1; ETag/304 for future polling optimization

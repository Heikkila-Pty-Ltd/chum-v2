---
topic: CHUM Dashboard UI Overhaul
date: 2026-03-12
status: complete
---

# CHUM Dashboard UI Overhaul

## What We're Building

A complete overhaul of the CHUM dashboard from 8 tabs down to 4, with each tab serving a distinct purpose:

1. **Overview** — Blockers-first kanban showing what's moving through the system
2. **Structure** — Filterable dependency graph showing how tasks link together
3. **Plans** — Chat-based grooming workspace with rich task preview and iterative refinement
4. **Jarvis** — KB view (kept as-is, rework separately later)

Current state: ~5,600 lines across 12 JS/HTML files + 2 CSS files. 8 tabs with significant overlap (stats duplicates overview hero, tree duplicates graph, tasks duplicates overview pipelines). ~300 lines dead CSS. Two separate planning UIs (detail panel console + Plans tab).

## Why This Approach

The dashboard has grown organically — each view was added independently without considering overlap. The result: 8 tabs where most users only need 3 workflows. The review identified:

- Stats view is a strict subset of Overview
- Tree and Graph show the same data differently
- Timeline has no actions, no filtering — purely informational
- Tasks view is a flat list that Overview covers better with goal grouping
- Planning console in detail panel duplicates Plans tab
- ~300 lines of dead CSS from previous iterations

Cut the noise. Each tab should have a clear, non-overlapping purpose.

## Key Decisions

### Overview: Blockers-first kanban with 5 columns

**Columns:** Backlog / Ready / Running / Review / Done

Status mapping:
- **Backlog**: open, decomposed
- **Ready**: ready
- **Running**: running
- **Review**: needs_review, needs_refinement, dod_failed
- **Done**: completed, done

Failed/rejected/stale tasks get a red accent indicator within whichever column they're in.

**Done column:** Collapsed by default or limited to last N completed tasks to avoid overwhelming the view. Toggle to show all.

**Layout (top to bottom):**
1. Action bar — blockers/actions requiring attention (absorbed from Jarvis). Resolve controls inline.
2. Focus strip — what Jarvis is working on now (current_focus, cycle info). Compact monospace row.
3. Hero stats — Total, Completion %, Running, Active Goals, Last 24h velocity.
4. Kanban board — 5 columns with small task cards (title + goal badge + time info). Click opens detail panel.

**Task cards in kanban:** Small cards showing title, goal color/badge, and time since last update. Failed tasks get a red left border. Click opens the existing slide-in detail panel.

**Data sources:** `/api/dashboard/overview-grouped/{project}` + `/api/dashboard/jarvis/summary` + `/api/dashboard/jarvis/actions` + `/api/dashboard/jarvis/state`

### Structure: Filterable dependency graph with 3 layout modes

**Layout modes** (toggle buttons):
- **Dagre** (default) — hierarchical DAG
- **Force** — force-directed
- **Outline** — indented tree (absorbed from tree.js)

**Filters** (new):
- Hide completed tasks (toggle, default on)
- Filter by goal/project
- Highlight critical path (longest chain of unfinished deps)

**Data:** `/api/dashboard/graph/{project}` for dagre/force, `/api/dashboard/tree/{project}` for outline.

Keep fingerprint-based refresh optimization from dag.js.

### Plans: Fix bugs + rich task preview

Keep the existing chat + pipeline flow (groom -> decompose -> approve -> materialize).

**Task preview improvements after decompose:**
- Show draft tasks as an indented tree (respecting depends_on) with estimate badges and batch numbers
- Show a small dependency graph of the draft tasks
- Click a task to select it, review its description/acceptance criteria
- Ability to chat further about a selected task, refine it, then redecompose

**Bug fixes:**
- `renderPipeline()` called but never defined (line 478) — runtime error
- Local `PlanAPI` bypasses `App.API` — consolidate
- Raw `fetch()` in `executePipelineAction` — use API client
- `simpleMarkdown` re-implements HTML escaping — use `App.escapeHtml`

### Detail panel: Keep but trim

Keep the slide-in panel. Remove:
- Entire planning console section (~180 lines from app.js)
- Redundant info that's already visible in the kanban card

Keep: task ID, title, status, description, meta grid, acceptance criteria, dependencies, dependents, code targets, decisions, PR/error section.

### Visual direction: Refine current theme

Keep dark surfaces, Space Grotesk/JetBrains Mono, warm gold accent. Tighten:
- More consistent spacing (use the --sp-* tokens consistently)
- Consistent component patterns across views (shared status bar, error states, section headers)
- Better card/surface hierarchy
- Consistent CSS naming (drop the ov2- prefix since ov- is freed up)

### What gets deleted

| File | Lines | Reason |
|------|-------|--------|
| stats.js | 102 | Redundant with overview hero |
| timeline.js | 139 | No filtering, no actions |
| tasks.js | 146 | Overview kanban covers this |
| tree.js | 202 | Absorbed into Structure outline mode |
| dag.js | 353 | Replaced by structure.js |
| Dead CSS (ov-*, timeline-*, stats-*, etc.) | ~300 | Never referenced |
| Planning console in app.js | ~180 | Plans tab is the planning UI |

## Open Questions

*None — all resolved during brainstorming.*

## Scope Boundaries

**In scope:**
- 4-tab navigation (Overview kanban, Structure graph, Plans, Jarvis)
- Shared utilities extraction (status bar, health color, error states)
- Dead code removal
- Plans bug fixes + task preview tree/graph
- Detail panel trimming
- CSS cleanup and refinement

**Out of scope (separate work):**
- Jarvis tab rework
- Backend API changes (all existing endpoints suffice)
- Mobile/responsive design

**Implementation notes:**
- Kanban data transform is client-side: re-index goal children by status column
- Critical path is client-side BFS through graph edges (no backend needed)
- Plans "refine task" works by prepending selected task context to the groom message
- Plans mini graph reuses D3/dagre already loaded for structure.js

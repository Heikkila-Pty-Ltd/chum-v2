---
title: "Work Page: Task & Epic Visibility"
date: 2026-03-24
type: brainstorm
status: complete
---

# Work Page: Task & Epic Visibility

## Problem

The CHUM dashboard has 5 pages (Check, Plan, Steer, Learn, Projects) full of summary metrics and aggregate views, but no way to actually examine what's being built. You can't:

- Open a task and read its description, acceptance criteria, or error log
- See why the UCT chose a particular execution path (decision history)
- Understand the epic→task→subtask hierarchy and progress
- View execution cost, duration, or trace for a specific task
- See code targets, dependencies, or related lessons

The backend already serves all of this via `/api/dashboard/task/{taskID}` and `/api/dashboard/tree/{project}`, but the frontend never calls these endpoints.

## What We're Building

A **master-detail Work page** that replaces the current Projects page (nav slot 5). Two-panel layout:

### Left Panel: Project Tree
- **Project picker dropdown** at top — select which project to view
- **Collapsible epic→task→subtask tree** below
- Tree nodes show: collapse toggle, status badge, title
- **Epic nodes show mini progress bars** (% of children completed)
- Click any node → loads detail in right panel

### Right Panel: Task Detail
Primary sections (always visible):
1. **Narrative** — title, description, acceptance criteria
2. **Decisions** — decision history with UCT scores, alternatives considered, which was chosen and why
3. **Relationships** — parent epic, child subtasks, dependency edges (from/to), code targets

Secondary sections (collapsible, below):
4. **Execution** — status, cost breakdown, estimate vs actual duration, iteration count, attempt count
5. **Error log** — full error text when failed
6. **Traces** — execution trace timeline (plan→exec→review stages)
7. **Lessons** — lessons learned linked to this task

## Why This Approach

- **Master-detail is the right pattern** for "browse hierarchy, inspect detail" workflows (file explorers, email clients, IDE project views)
- **Project picker + tree** keeps it focused — one project at a time, not overwhelmed by everything
- **Progress bars on epics** give at-a-glance "how's this going" without clicking
- **Narrative + decisions front-and-center** matches the stated goal: "the what and why"
- **Backend already serves everything** — no new API endpoints needed

## Key Decisions

1. **Replaces Projects page** — Projects health cards are useful but secondary to task visibility. The project picker + tree subsumes the project selection role. Health summary could appear as a compact header above the tree.
2. **Narrative + decisions + relationships are primary** — execution/cost/traces are secondary (collapsible). The story matters more than the metrics.
3. **Project picker, not unified tree** — one project at a time keeps it clean and fast.
4. **Epics show progress bars** — tree density is "status badge + progress bar", not minimal and not overloaded.

## Resolved Questions

1. **What happens to project health cards?** → **Drop them.** Check page already covers health. The tree itself shows progress via epic progress bars. No duplication.
2. **Should clicking a task in other pages navigate to Work page?** → **Yes, link through.** Task IDs/titles in Check feed, Steer queue, etc. become clickable links that navigate to `#/work?task={id}` with that task pre-selected in the tree and detail panel.
3. **Search/filter within the tree?** → **Yes, filter bar.** Text input above the tree that filters visible nodes by title or status. Necessary for projects with many tasks.

## Rejected Alternatives

- **Inline expansion (Approach B)**: Gets unwieldy with multiple expanded tasks, harder to compare, loses the clean hierarchy view.
- **Slide-out overlay (Approach C)**: Loses side-by-side context, and the user explicitly wanted a dedicated page not a panel.
- **Flat task list with filters**: Loses the hierarchy narrative entirely.

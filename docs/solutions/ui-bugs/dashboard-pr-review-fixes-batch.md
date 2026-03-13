---
module: Dashboard
date: 2026-03-13
problem_type: ui_bug
component: assistant
symptoms:
  - "Plans tab chat renders blank replies (SSE client vs JSON backend)"
  - "Structure goal filter shows wrong tasks and breaks on project switch"
  - "Overview crashes entirely when Jarvis API is unavailable"
  - "Critical path highlights nothing on zero-edge graphs"
  - "CI fmt-check rejects unformatted context.go / format.go"
root_cause: wrong_api
resolution_type: code_fix
severity: high
tags: [dashboard, plans-tab, structure-view, overview, sse, dag-edges, promise-allsettled, critical-path, gofmt]
---

# Dashboard PR Review Fixes Batch

## Problem

PR #76 (dashboard UI overhaul + planner codebase context injection) received 7 review findings across 3 views — 2 from a human reviewer (doctorspritz), 5 from Codex bot. Plus a `quality` CI gate failure from unformatted Go files.

**Symptoms:**
- Plans tab chat renders blank replies (SSE client vs JSON backend)
- Structure goal filter shows wrong tasks and breaks on project switch
- Overview crashes entirely when Jarvis API is unavailable
- Critical path highlights nothing on zero-edge graphs
- CI `fmt-check` rejects unformatted `context.go` / `format.go`

## Root Causes

**7 distinct issues, 3 root causes:**

1. **Contract mismatch**: `plans.js` `sendMessage()` was ported from the old `/groom` SSE endpoint but now calls `/interview` which returns plain JSON via `jsonOK()`. The SSE stream reader (`getReader()` + `data:` parsing) silently consumed zero matching lines.

2. **Semantic confusion**: `structure.js` treated DAG edges as parent->child, but `edges.go:L9` defines them as dependent->prerequisite (`from_task` depends on `to_task`). This inverted `populateGoalFilter` (showed leaf tasks as goals), `filterNodes` (traversed up instead of down), and `computeCriticalPath` (swapped children/parents). Even after fixing edge direction, the goal filter was still using dependency edges instead of the `parent_id` task hierarchy.

3. **Missing resilience**: Overview `Promise.all` included 3 optional Jarvis API calls alongside the required overview data, so any Jarvis failure rejected the entire render. `FormatForPrompt` had no cap on `allFiles` (could inject thousands of file lines into LLM prompt). `computeCriticalPath` initialized `maxDist=0` so isolated nodes (distance 0) were never selected.

## Solution

### Plans tab (P1): JSON instead of SSE

```javascript
// Before: SSE stream reader that never finds data: lines
const reader = res.body.getReader();
// ... 35 lines of SSE parsing ...

// After: simple JSON parse
const plan = await res.json();
const fullText = plan.planner_reply || '';
if (currentPlan) {
  Object.assign(currentPlan, plan);
  renderPipeline();
}
```

### Structure goal filter (P2): Use parent_id hierarchy

```javascript
// Before: dependency-edge roots (wrong — shows leaf tasks)
const targets = new Set(data.edges.map(e => e.to));
const roots = data.nodes.filter(n => !targets.has(n.id));

// After: actual goals via task hierarchy
const goals = data.nodes.filter(n => !n.parent_id);
```

Goal subtree traversal also switched from edge-based to `parent_id`-based:

```javascript
// Before: traverses dependency edges (goes to prerequisites)
if (descendants.has(e.to) && !descendants.has(e.from)) ...

// After: traverses parent_id tree (goes to children)
if (n.parent_id && descendants.has(n.parent_id) && !descendants.has(n.id)) ...
```

### Structure state reset on project switch

```javascript
function render(viewport, project) {
  goalFilter = '';        // clear stale filter from previous project
  cachedData = null;
  cachedTreeData = null;
  // ...
}
```

### Goal filter rebuild on refresh

Removed `if (select.options.length > 1) return` guard. Now clears and rebuilds options each time, preserving previous selection if still valid.

### Overview Jarvis resilience (P1)

```javascript
// Before: any Jarvis error kills the whole overview
const [grouped, summary, actions, state] = await Promise.all([...]);

// After: Jarvis calls are optional
const [grouped, jarvisResults] = await Promise.all([
  App.API.overviewGrouped(project),
  Promise.allSettled([
    App.API.jarvisSummary(),
    App.API.jarvisActions(),
    App.API.jarvisState(),
  ]),
]);
const summary = jarvisResults[0].status === 'fulfilled' ? jarvisResults[0].value : {};
```

### Codebase map cap

```go
const maxDirTreeFiles = 80

allFiles := collectAllFiles(r)
if len(allFiles) > maxDirTreeFiles {
    allFiles = allFiles[:maxDirTreeFiles]
}
```

### Critical path zero-edge fix

```javascript
// Before: maxDist=0 means isolated nodes (d=0) never qualify
let maxDist = 0, endNode = null;

// After: -1 threshold includes zero-distance nodes
let maxDist = -1, endNode = null;
```

### gofmt

`context.go` and `format.go` had tab/space formatting inconsistencies caught by CI `fmt-check`.

## Prevention

- **When changing an API endpoint the client calls, verify the response format matches.** SSE and JSON are fundamentally different wire formats — a client written for one silently fails on the other.
- **When a graph/DAG has documented edge conventions (`edges.go:L9`), reference those comments in client code.** The `from=dependent, to=prerequisite` convention was documented in Go but never mentioned in JS.
- **Use `Promise.allSettled` for optional API calls** that shouldn't block the primary render path.
- **Cap unbounded collections before injecting into LLM prompts.** The `AllFiles` fallback path can contain the entire repo.
- **Run `gofmt` locally before pushing** — or add it to the pre-push hook alongside `go build` and `go test`.

## Resources

- PR: https://github.com/Heikkila-Pty-Ltd/chum-v2/pull/76
- Edge convention: `internal/dag/edges.go:L9`
- Related solution: `docs/solutions/ui-bugs/chum-dashboard-plans-tab-document-rendering.md`
- Related solution: `docs/solutions/integration-issues/planner-codebase-context-injection.md`

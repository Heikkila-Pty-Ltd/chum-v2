# Critical Patterns — Required Reading

These patterns were extracted from real incidents. Violating them causes bugs that are hard to diagnose. All subagents should review before generating code.

---

## 1. Verify Transport Contract When Changing API Routes (ALWAYS REQUIRED)

### ❌ WRONG (Blank responses, silent data loss)
```javascript
// Frontend calls a JSON endpoint but parses as SSE
const res = await fetch(`/api/dashboard/plan/${id}/interview`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ message }),
});

const reader = res.body.getReader();
// ... SSE parsing that silently finds zero "data:" lines
```

### ✅ CORRECT
```javascript
// Match the parser to the endpoint's actual response format
const res = await fetch(`/api/dashboard/plan/${id}/interview`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ message }),
});

if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
const plan = await res.json(); // /interview returns JSON, not SSE
```

**Why:** SSE and JSON are fundamentally different wire formats. An SSE parser silently produces empty results on JSON (no `data:` lines to match). The bug is invisible — no errors, just blank UI.

**Placement/Context:** Any time a frontend route changes to a different backend endpoint, or a backend handler changes its response format. Check `Content-Type` and handler implementation (`jsonOK` vs `text/event-stream`).

**Documented in:** `docs/solutions/ui-bugs/dashboard-pr-review-fixes-batch.md`, `docs/solutions/integration-issues/plans-chat-transport-contract-regression-20260313.md`

---

## 2. Use SDK Error Helpers, Not Inline gRPC Status Checks (ALWAYS REQUIRED)

### ❌ WRONG (Crash-loop on every restart)
```go
// Raw gRPC check misses SDK-wrapped errors
_, err := client.ScheduleClient().Create(ctx, options)
if err != nil {
    if status.Code(err) != codes.AlreadyExists {
        return fmt.Errorf("create schedule %q: %w", options.ID, err)
    }
}
```

### ✅ CORRECT
```go
// Shared helper checks all three error representations
_, err := client.ScheduleClient().Create(ctx, options)
if err != nil {
    if !isScheduleAlreadyExists(err) {
        return fmt.Errorf("create schedule %q: %w", options.ID, err)
    }
}

func isScheduleAlreadyExists(err error) bool {
    if err == nil { return false }
    var alreadyExistsErr *serviceerror.AlreadyExists
    return errors.Is(err, temporal.ErrScheduleAlreadyRunning) ||
        errors.As(err, &alreadyExistsErr) ||
        status.Code(err) == codes.AlreadyExists
}
```

**Why:** The Temporal Go SDK wraps gRPC errors in its own types (`serviceerror.AlreadyExists`). A raw `status.Code()` check only works on unwrapped gRPC errors. When the SDK wraps the error, status code extraction fails and the error falls through to the fatal path, crash-looping the service on every restart.

**Placement/Context:** All Temporal schedule upsert logic. Copy from `internal/schedules/content_update.go` (correct), never from `daily_collection.go` (had the bug).

**Documented in:** `docs/solutions/runtime-errors/schedule-already-exists-temporal-20260312.md` (hg-chum-integration)

---

## 3. Progressive Disclosure for LLM Context, Not Full Dumps (ALWAYS REQUIRED)

### ❌ WRONG (Prompt overflow, "Prompt is too long" errors)
```go
// Dump entire AST into the prompt — 200K+ chars for a 150-file project
allFiles := ast.ParseAll(workDir)
prompt := systemPrompt + "\n" + formatFullAST(allFiles) + "\n" + userMessage
```

### ✅ CORRECT
```go
// Layer 1: Directory map (one line per file, ~5K chars)
// Layer 2: Signatures for query-matched files only (~5K chars)
// Total: ~10K chars for a 150-file project
ctxResult := codebase.Build(ctx, codebase.BuildOpts{
    Parser: a.AST, Store: a.Store, DAG: a.DAG,
    WorkDir: workDir, Query: briefContext + " " + message,
})
ctxFormatted := codebase.FormatForPrompt(ctxResult)

// Cap unbounded fallbacks
const maxDirTreeFiles = 80
if len(allFiles) > maxDirTreeFiles {
    allFiles = allFiles[:maxDirTreeFiles]
}
```

**Why:** Full AST dumps of Go projects (150+ files) exceed LLM context windows. Progressive disclosure — directory map + signatures for relevant files only — keeps context compact (~10K chars vs 200K+) while still grounding the LLM in the actual codebase.

**Placement/Context:** Any LLM-powered feature that needs codebase awareness. Use `internal/codebase` package with `FormatForPrompt` for planning, `FormatAST` only for task execution where the agent writes code.

**Documented in:** `docs/solutions/integration-issues/planner-codebase-context-injection.md`

---

## 4. Validate Frontend Against Actual Backend Structs (ALWAYS REQUIRED)

### ❌ WRONG (Silent rendering failures)
```javascript
// Speculatively assumes struct fields that don't exist
const epics = draftTasks.filter(t => t.type === 'epic');     // NO type field
const children = task.children || [];                          // NO children field
```

### ✅ CORRECT
```javascript
// Use only fields that exist in the Go struct
// DraftTask has: ref, title, description, acceptance, estimate_minutes, batch, depends_on
const batches = {};
draftTasks.forEach(t => {
  const b = t.batch || 0;
  if (!batches[b]) batches[b] = [];
  batches[b].push(t);
});
```

**Why:** When frontend is built speculatively without reading the backend struct definition, it filters/renders on nonexistent fields. JavaScript doesn't error on `undefined.type` comparisons — it just silently produces empty results. The UI appears broken with no error messages.

**Placement/Context:** Before building any UI that consumes API data, read the Go struct definition and use only fields that actually exist. Check for unused backend fields that the UI should render (e.g., `brief_markdown`, `working_markdown`).

**Documented in:** `docs/solutions/ui-bugs/chum-dashboard-plans-tab-document-rendering.md`

---

## 5. DAG Edge Convention: from=dependent, to=prerequisite (ALWAYS REQUIRED)

### ❌ WRONG (Inverted goal filters, broken critical path)
```javascript
// Treating edges as parent→child (wrong direction)
const targets = new Set(data.edges.map(e => e.to));
const roots = data.nodes.filter(n => !targets.has(n.id));  // Shows leaves, not roots
```

### ✅ CORRECT
```javascript
// edges.go:L9 defines from=dependent, to=prerequisite
// Use parent_id for hierarchy, edges for dependencies
const goals = data.nodes.filter(n => !n.parent_id);

// Subtree traversal via parent_id (not edges)
if (n.parent_id && descendants.has(n.parent_id) && !descendants.has(n.id)) {
  descendants.add(n.id);
}
```

**Why:** `internal/dag/edges.go:L9` defines edges as `from_task` depends on `to_task`. Treating them as parent→child inverts goal filters (shows leaf tasks as goals), subtree traversal (goes to prerequisites instead of children), and critical path (swaps start/end). Use `parent_id` for hierarchy, edges only for dependency ordering.

**Placement/Context:** Any code that reads DAG edges in `structure.js` or similar views. Always reference the edge convention comment in `edges.go`.

**Documented in:** `docs/solutions/ui-bugs/dashboard-pr-review-fixes-batch.md`

---

## 6. Use Promise.allSettled for Optional API Calls (ALWAYS REQUIRED)

### ❌ WRONG (Entire view crashes when optional service is down)
```javascript
// Any Jarvis failure kills the whole overview
const [grouped, summary, actions, state] = await Promise.all([
  App.API.overviewGrouped(project),
  App.API.jarvisSummary(),    // optional
  App.API.jarvisActions(),    // optional
  App.API.jarvisState(),      // optional
]);
```

### ✅ CORRECT
```javascript
// Required calls separate from optional ones
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

**Why:** `Promise.all` rejects on the first failure. If optional API calls (Jarvis, analytics, etc.) are mixed with required data fetches, the entire view breaks when the optional service is unavailable. `Promise.allSettled` lets optional calls fail gracefully.

**Placement/Context:** Any dashboard view that combines required data with optional enrichment from separate services.

**Documented in:** `docs/solutions/ui-bugs/dashboard-pr-review-fixes-batch.md`

---

## 7. Content-Based IDs Through Pipeline Stages (ALWAYS REQUIRED)

### ❌ WRONG (Silent data corruption on reorder)
```python
# Positional index as ID — breaks if extraction order changes
facts = [{"id": i, "claim": claim} for i, claim in enumerate(extracted)]
# research stage assumes fact[0] is still the same claim
```

### ✅ CORRECT
```python
# Content-based ID computed once, carried as opaque identifier
import hashlib

def fact_id(claim_text: str) -> str:
    return hashlib.sha256(claim_text.encode()).hexdigest()[:12]

facts = [{"id": fact_id(claim), "claim": claim} for claim in extracted]
# research and cross-check stages match on stable ID
```

**Why:** When data flows through multiple pipeline stages (extract → research → cross-check), positional indices silently corrupt if the extraction order changes between runs. Content-based IDs are stable across reruns and make cross-stage joins reliable.

**Placement/Context:** Any multi-stage pipeline where data is produced in one stage and consumed in another. Compute the ID once in the first stage, never recompute downstream.

**Documented in:** `docs/solutions/integration-issues/decompose-monolithic-temporal-activity-into-pipeline.md` (hg-content-update)

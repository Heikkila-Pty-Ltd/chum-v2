---
title: Missing Plan Document Rendering in CHUM Dashboard Plans Tab
category: ui-bugs
date: 2026-03-12
severity: high
component: chum-dashboard
module: plans-view
tags: [document-rendering, data-model-mismatch, frontend-backend-contract]
symptoms:
  - Plan document content (brief_markdown, working_markdown) not displayed in UI
  - DraftTask fields mismatch between Go struct and JavaScript assumptions
  - Task tree rendering fails silently due to nonexistent type/children fields
  - No way to view structured plan spec separate from chat conversation
root_cause: Plan document fields existed in backend data model but were never rendered in UI; JavaScript task rendering assumed struct fields that don't exist in Go backend
---

# Missing Plan Document Rendering in CHUM Dashboard Plans Tab

## Problem

The CHUM dashboard Plans tab was missing any way to view the structured plan document. The UI only showed the chat conversation and draft tasks, but the core output of grooming — the plan spec itself — was invisible.

Two distinct issues:

1. **Missing document surface**: `PlanDoc` has `brief_markdown`, `working_markdown`, and `structured` fields that were never rendered
2. **DraftTask field mismatch**: JavaScript assumed `type` (epic/subtask) and `children` fields that don't exist in the Go struct

## Root Cause Analysis

The frontend was built speculatively without validating against the actual backend data model.

**Go backend (`internal/dag/plan.go`):**
```go
type PlanDoc struct {
    BriefMarkdown    string          `json:"brief_markdown"`
    WorkingMarkdown  string          `json:"working_markdown"`
    Structured       json.RawMessage `json:"structured"`
    // ... other fields
}

type DraftTask struct {
    Ref             string   `json:"ref"`
    Title           string   `json:"title"`
    Description     string   `json:"description"`
    Acceptance      string   `json:"acceptance"`
    EstimateMinutes int      `json:"estimate_minutes"`
    Batch           int      `json:"batch"`
    DependsOn       []string `json:"depends_on"`
    // NO type, NO children fields
}
```

**JavaScript assumed (incorrectly):**
- `t.type === 'epic'` / `t.type === 'subtask'` — field doesn't exist
- `t.children` array — field doesn't exist
- Hierarchical epic/subtask tree structure — backend is flat with batch grouping

## Solution

### 1. Added Chat/Plan tabbed view

New `activeMainTab` state switches between chat conversation and plan document:

```javascript
let activeMainTab = 'chat';

// Status bar now has tab buttons
<div class="plans-main-tabs">
  <button class="plans-main-tab" data-main-tab="chat">Chat</button>
  <button class="plans-main-tab" data-main-tab="document">Plan</button>
</div>

// Two panes, shown/hidden by active tab
<div class="plans-main-pane" data-main-pane="chat">...</div>
<div class="plans-main-pane" data-main-pane="document">...</div>
```

### 2. New `renderPlanDocument(plan)` function

Renders all three plan content fields with appropriate formatting:

```javascript
function renderPlanDocument(plan) {
  const brief = plan.brief_markdown || '';
  const working = plan.working_markdown || '';

  if (!brief && !working) {
    return '<div class="plans-doc-empty">No plan document yet...</div>';
  }

  let html = '';
  if (brief) {
    html += `<div class="plans-doc-section">
      <div class="plans-doc-section-label">Brief</div>
      <div class="plans-doc-content">${simpleMarkdown(brief)}</div>
    </div>`;
  }
  if (working) {
    html += `<div class="plans-doc-section">
      <div class="plans-doc-section-label">Working Spec</div>
      <div class="plans-doc-content">${simpleMarkdown(working)}</div>
    </div>`;
  }
  // Plus structured JSON if present
}
```

### 3. Fixed DraftTask rendering — batch grouping instead of type hierarchy

```javascript
// OLD (broken): filtered by nonexistent type field
const epics = draftTasks.filter(t => t.type === 'epic');

// NEW (correct): group by actual batch field
const batches = {};
draftTasks.forEach(t => {
  const b = t.batch || 0;
  if (!batches[b]) batches[b] = [];
  batches[b].push(t);
});
```

### 4. Updated renderPipeline() to refresh document pane

```javascript
const docEl = document.querySelector('[data-plans-document]');
if (docEl) {
  docEl.innerHTML = renderPlanDocument(currentPlan);
}
```

## Files Changed

| File | Change |
|------|--------|
| `web/views/plans.js` | Added renderPlanDocument(), bindMainTabEvents(), activeMainTab state; fixed renderTaskTree, renderTaskPreview, renderDepGraph |
| `web/style_plans.css` | Added .plans-main-tabs, .plans-main-pane, .plans-document, .plans-doc-*, .plans-task-batch-header |

## Prevention Strategies

### Validate frontend against actual backend structs
Before building UI that consumes API data, read the Go struct definition and use only fields that actually exist. Don't speculatively add fields you think should be there.

### Check for unused backend fields
When reviewing a feature, ask: "Are there fields in the API response that the UI doesn't render?" This catches the `brief_markdown`/`working_markdown` omission pattern.

### Defensive rendering
Use optional chaining and guard clauses for fields that may not exist. Don't filter on assumed type fields — check what the data actually contains.

## Related Documentation

- **Brainstorm**: `docs/brainstorms/2026-03-12-chum-dashboard-ui-overhaul-brainstorm.md`
- **Plan**: `docs/plans/2026-03-11-001-feat-plans-guided-grooming-workspace-plan.md`
- **Backend model**: `internal/dag/plan.go` (PlanDoc and DraftTask structs)

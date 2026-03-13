---
module: Planner
date: 2026-03-12
problem_type: integration_issue
component: assistant
symptoms:
  - "Planner LLM asks generic questions like 'What web framework are you using?' instead of grounding in actual code"
  - "Prompt is too long errors when full AST dump injected into LLM context"
  - "No codebase awareness in handlePlanInterview or handlePlanDecompose endpoints"
root_cause: wrong_api
resolution_type: code_fix
severity: medium
tags: [planner, llm, context-injection, progressive-disclosure, ast, prompt-engineering]
---

# Planner LLM produces generic responses without codebase context

## Problem

The CHUM planner's interview and decompose endpoints (`handlePlanInterview`, `handlePlanDecompose`) called the LLM with only the plan brief and conversation history. The LLM had no knowledge of the project's codebase structure, existing patterns, active tasks, or conventions. This caused it to ask generic questions like "What web framework are you using?" instead of grounding its responses in the actual code.

The execution engine (`internal/engine/activities.go`) already had a full context pipeline (AST parsing, embeddings, keyword filtering) but the planner never used it.

## Root Cause

The planner endpoints were built as standalone chat handlers that only passed the plan document to the LLM. The existing `buildCodebaseContextForTask` function in the engine was tightly coupled to task execution and couldn't be reused by the planner API.

Additionally, the full AST dump of a Go project (~150 files) exceeded the LLM's context window, causing "Prompt is too long" errors when naively injected.

## Solution

### 1. Shared codebase context package (`internal/codebase/`)

Extracted the context-gathering pipeline into a reusable package with two files:

**`context.go`** — `Build(ctx, BuildOpts) *ContextResult` gathers from 4 sources:
- AST + embeddings (via `ast.NewEmbedFilter().FilterRelevantByEmbedding`) with keyword fallback
- Lessons FTS5 search (`store.SearchLessons`)
- DAG active tasks (`dag.ListTasks` with active statuses)
- CLAUDE.md from project workspace

Each source is best-effort — failures are logged but never block planning.

**`format.go`** — `FormatForPrompt(*ContextResult) string` uses progressive disclosure:
- **Layer 1**: Codebase directory map (file paths, packages, exported symbol counts). Every file gets one line. Files matching the query are marked with `*`.
- **Layer 2**: Exported type/function signatures for query-matched files only (not full source).
- CLAUDE.md (truncated to ~2000 chars), active tasks grouped by goal, and lessons as compact bullets.

This keeps the context compact (~10K chars for a 150-file project) vs the full AST dump (~200K+ chars).

### 2. Context caching in PlanDoc

Added `context_snapshot` column to `plan_docs` table (via `migratePlanContextSnapshot` in `schema.go`). First interview turn gathers context and caches it. Subsequent turns reuse the cached snapshot, avoiding redundant AST parsing and embedding calls.

### 3. Handler integration

Both `handlePlanInterview` and `handlePlanDecompose` in `plans_backend.go`:
1. Resolve `workDir` via `Engine.WorkDir(project)`
2. Check for cached `context_snapshot` on the PlanDoc
3. If empty, call `codebase.Build()` and cache the result
4. Inject the formatted context into the LLM prompt between the system prompt and the plan brief

### Key code pattern

```go
// Gather codebase context (first turn) or reuse cached.
var ctxFormatted string
if plan.ContextSnapshot != "" {
    ctxFormatted = plan.ContextSnapshot
} else {
    ctxResult := codebase.Build(r.Context(), codebase.BuildOpts{
        Parser:  a.AST,
        Store:   a.Store,
        DAG:     a.DAG,
        Logger:  a.Logger,
        WorkDir: workDir,
        Project: plan.Project,
        Query:   briefContext + " " + body.Message,
    })
    ctxFormatted = codebase.FormatForPrompt(ctxResult)
    if ctxFormatted != "" {
        _ = a.DAG.UpdatePlanFields(r.Context(), id, map[string]any{
            "context_snapshot": ctxFormatted,
        })
    }
}
```

## Gotchas

1. **Prompt size**: Full AST dumps of Go projects blow the prompt. Always use progressive disclosure — directory map + signatures, never full source for planning.

2. **Bare repo vs worktree**: If the project uses git worktrees, `workDir` must point to the worktree path, not the bare repo. The bare repo has no working tree to parse.

3. **Duplicate handler files**: The worktree had 3 overlapping plan handler files (`plan_handlers.go`, `plans_backend.go`, `plans_api.go`). Adding context injection to the wrong file caused redeclaration errors. Always check `go build ./...` after editing.

4. **UpdatePlan vs UpdatePlanFields**: The worktree's DAG has both `UpdatePlan(ctx, *PlanDoc)` (struct-based) and `UpdatePlanFields(ctx, id, map[string]any)` (field-based). The planner handlers use `UpdatePlanFields` — using the wrong one causes compilation errors.

5. **AllFiles fallback**: When no embedding or keyword matches are found, the `AllFiles` fallback can dump hundreds of files. Cap `FormatAST()` output or limit `AllFiles` to prevent prompt overflow.

## Prevention

- When adding LLM-powered features that need codebase awareness, always use the shared `internal/codebase` package rather than building ad-hoc context gathering.
- Use `FormatForPrompt` (progressive disclosure) for planning prompts, and `FormatAST` (full source) only for task execution where the agent needs to write code.
- Test with a real project (not toy examples) to catch prompt size issues early.
- Add the `context_snapshot` pattern (gather-once, cache, reuse) whenever an LLM conversation spans multiple turns over the same codebase.

## Files Changed

- `internal/codebase/context.go` — new shared context builder
- `internal/codebase/format.go` — progressive disclosure formatter
- `internal/jarvis/plans_backend.go` — added context injection to interview + decompose
- `internal/jarvis/api.go` — added `AST *ast.Parser` field
- `internal/jarvis/jarvis.go` — added `WorkDir()` accessor
- `internal/dag/plan_store.go` — added `ContextSnapshot` field, updated SQL
- `internal/dag/schema.go` — added `migratePlanContextSnapshot` migration
- `cmd/dashboard-preview/main.go` — injected `ast.NewParser(logger)`

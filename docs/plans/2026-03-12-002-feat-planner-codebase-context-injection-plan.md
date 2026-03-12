---
title: "feat: Planner Codebase Context Injection"
type: feat
status: completed
date: 2026-03-12
origin: docs/brainstorms/2026-03-12-planner-codebase-context-injection-brainstorm.md
---

# feat: Planner Codebase Context Injection

## Overview

Enrich the CHUM planner's `handlePlanInterview` and `handlePlanDecompose` endpoints with codebase context so the planning agent produces plans that reference real files, follow existing patterns, and avoid duplicating work. Currently both endpoints are blind single-shot LLM calls with zero codebase knowledge.

The codebase already has a complete context pipeline (AST parser, vector search, keyword fallback, FTS5 lessons) used for task execution — the planner just doesn't use any of it. This plan extracts the shared logic and wires it into the planning endpoints.

(see brainstorm: `docs/brainstorms/2026-03-12-planner-codebase-context-injection-brainstorm.md`)

## Problem Statement / Motivation

Planning is cheap relative to implementing the wrong thing. A planner that understands the codebase produces plans that reference real files, follow existing patterns, avoid duplicating work, and account for architectural constraints. The difference between "add authentication" and "add authentication using the existing SessionStore pattern in `internal/auth/` with the middleware chain in `cmd/server/middleware.go`" is the difference between useful and not.

Currently `handlePlanInterview` and `handlePlanDecompose` in `internal/jarvis/plans_api.go`:
- Pass hardcoded `"."` as workDir to `a.LLM.Plan()`
- Include zero codebase context (no file tree, no code, no conventions)
- Have no awareness of existing DAG goals/tasks (risk of duplicating work)
- Don't consult past learnings/solutions

## Proposed Solution

Reuse the existing AST + embedding + keyword + FTS5 pipeline, extracted into a shared package, and wire it into both planning endpoints.

### Architecture

**Extract shared context builder.** `buildCodebaseContextForTask` exists on both `engine.Activities` (line 387) and `planning.PlanningActivities` (line 599) — already duplicated. Extract into a new shared package `internal/codebase` with a standalone function:

```go
// internal/codebase/context.go
package codebase

type ContextResult struct {
    RelevantFiles   []*ast.ParsedFile // full source
    SurroundingFiles []*ast.ParsedFile // signatures only
    Lessons         []store.Lesson
    DAGSummary      string
    ClaudeMD        string
    FileTree        string
}

func BuildContext(ctx context.Context, opts BuildContextOpts) (*ContextResult, error)

type BuildContextOpts struct {
    Parser      *ast.Parser
    EmbedFilter *ast.EmbedFilter
    Store       *store.Store      // for lessons FTS5
    DAG         *dag.DAG          // for active goals/tasks
    WorkDir     string
    Project     string
    Query       string            // plan brief + user messages
}
```

**Wire into `jarvis.API`.** Add `AST *ast.Parser` field to `jarvis.API` struct (injected at construction). Add `WorkDir(project string) string` accessor to Engine (since `workDirs` map is unexported).

**Context sources** (in order of value, see brainstorm):

1. **Fix workDir** — pass actual project workspace path instead of `"."`. Add `WorkDir(project string) string` accessor to Engine.
2. **AST + embeddings** — `ast.EmbedFilter.FilterRelevantByEmbedding()` with keyword fallback via `ast.FilterRelevant()`. Relevant files get full source, surrounding files get signatures-only.
3. **DAG active goals + tasks** — `dag.DAG.ListTasks(ctx, project, activeStatuses...)` filtered to active statuses (ready, running, needs_review, needs_refinement, dod_failed, failed). Summarized as text.
4. **Lessons FTS5** — `store.SearchLessons(query, limit)` with sanitized keywords from plan brief + conversation. Limit ~5 results.
5. **CLAUDE.md** — `os.ReadFile(filepath.Join(workDir, "CLAUDE.md"))` injected directly into the prompt.

### Context Gathering Timing

**First turn + cache.** (see brainstorm)

- Gather context on the first user message (when topic keywords are available)
- Cache in PlanDoc via new field `context_snapshot` (stored as JSON text in SQLite)
- Subsequent turns reuse cached context
- User can trigger refresh via a "refresh context" action or message

Rationale: codebase rarely changes during a 5-15 message planning session. First turn is when we know what the plan is about. Avoids per-turn latency.

### Token Compression

**Native Go compression** — no RTK dependency. (see brainstorm)

- `truncateStr(s, n)` helper already exists in `dashboard_api.go`
- AST pipeline already does signatures-only for surrounding files
- Cap file tree depth, limit git log entries, truncate long descriptions
- Group files by directory for tree display

### Token Budget

Per interview/decompose call (~3600 new tokens):
- System prompt: ~600 tokens (existing)
- CLAUDE.md: ~500 tokens (new, truncated)
- Relevant code (full source, top files): ~2000 tokens (new)
- Surrounding code (signatures only): ~500 tokens (new)
- DAG active goals + tasks summary: ~300 tokens (new)
- Lessons matches: ~300 tokens (new)

## Implementation Phases

### Phase 1: Extract shared context builder + fix workDir

- [x] Create `internal/codebase/context.go` with `BuildContext()` function
- [ ] Create `internal/codebase/context_test.go` with unit tests
- [x] Add `WorkDir(project string) string` accessor to `internal/jarvis/jarvis.go` (Engine lives here, not engine.go)
- [x] Refactor `engine.Activities.buildCodebaseContextForTask()` to call `codebase.Build()`
- [x] Refactor `planning.PlanningActivities.buildCodebaseContextForTask()` to call `codebase.Build()`
- [x] Verify existing task execution still works with refactored context builder

### Phase 2: Wire context into planning endpoints

- [x] Add `AST *ast.Parser` field to `jarvis.API` struct in `internal/jarvis/api.go`
- [x] Update API construction site to inject AST parser (cmd/dashboard-preview/main.go)
- [x] Add `context_snapshot` field to `PlanDoc` struct in `internal/dag/plan.go`
- [x] Update SQLite schema for `context_snapshot` column (migrateAddColumn)
- [x] Modify `handlePlanInterview` to:
  - On first turn (no `context_snapshot`): call `codebase.Build()` with plan brief + user message as query
  - Cache result in `context_snapshot`
  - On subsequent turns: deserialize cached context
  - Inject context into prompt before LLM call
  - Pass real workDir instead of `"."`
- [x] Modify `handlePlanDecompose` to:
  - Call `codebase.Build()` (or use cached from interview)
  - Inject context into decompose prompt
  - Pass real workDir instead of `"."`

### Phase 3: Context formatting + prompt integration

- [x] Create `internal/codebase/format.go` with context-to-prompt formatter
- [x] Format relevant files as fenced code blocks with file paths
- [x] Format surrounding files as signature summaries
- [x] Format DAG summary as structured text (goal → children list)
- [x] Format lessons as bullet points with titles
- [x] Truncate CLAUDE.md to ~500 tokens
- [x] Add formatted context section to interview system prompt
- [x] Add formatted context section to decompose system prompt
- [ ] Test end-to-end: create a plan via dashboard, verify context appears in LLM prompt

## Technical Considerations

### Error Handling

- Context gathering is **best-effort** — if AST parsing fails, embeddings are unavailable, or DAG query errors, proceed without that context source. Never block planning on context failure.
- Each context source should have independent error handling with `slog.Warn` logging.
- `EmbedFilter` already has keyword fallback when Ollama is unavailable.

### Concurrency

- `ast.Parser.ParseDir()` is safe for concurrent use (mtime cache is read-heavy).
- `ast.EmbedFilter.FilterRelevantByEmbedding()` calls Ollama — may be slow on first call but caching mitigates per-turn latency.
- Context gathering on first turn may add 2-5 seconds latency. The first-turn + cache approach ensures this cost is paid once.

### FTS5 Query Sanitization

- FTS5 special characters (`*`, `"`, `(`, `)`, `NEAR`, `AND`, `OR`, `NOT`) must be escaped or stripped from the query.
- Extract keywords by tokenizing plan brief + conversation, filtering stop words, taking top N terms.

### Schema Migration

- New `context_snapshot TEXT` column on plans table — nullable, no migration needed for existing rows.
- Content is JSON-serialized `ContextResult` struct.

## Acceptance Criteria

- [ ] `handlePlanInterview` receives codebase context (relevant files, CLAUDE.md, DAG state, lessons) on first turn
- [ ] Context is cached in `context_snapshot` and reused on subsequent turns
- [ ] `handlePlanDecompose` receives codebase context
- [ ] Real workDir is passed instead of hardcoded `"."`
- [ ] `buildCodebaseContextForTask` is extracted from both `engine.Activities` and `planning.PlanningActivities` into shared `internal/codebase` package
- [ ] Context gathering failures are logged but don't block planning
- [ ] Token budget stays within ~3600 new tokens per call
- [ ] Existing task execution (via engine.Activities) still works after refactoring

## Dependencies & Risks

**Dependencies:**
- Ollama must be running for embedding-based file relevance (keyword fallback exists)
- `ast.Parser` requires tree-sitter Go grammar (already bundled)
- DAG access requires project name resolution

**Risks:**
- **Latency on first turn**: AST parsing + embedding may take 2-5 seconds on large codebases. Mitigated by first-turn + cache approach.
- **Stale context**: Cached context won't reflect mid-session code changes. Acceptable tradeoff — planning sessions are short (5-15 messages). Refresh mechanism planned.
- **Token overflow**: Large codebases with many relevant files could exceed budget. Mitigated by existing `maxRelevantFiles = 30` limit and signatures-only for surrounding files.

## Sources & References

- **Origin brainstorm:** [docs/brainstorms/2026-03-12-planner-codebase-context-injection-brainstorm.md](docs/brainstorms/2026-03-12-planner-codebase-context-injection-brainstorm.md) — Key decisions: reuse existing pipeline, extract to shared utility, native Go compression, first-turn + cache, include DAG state, fix workDir, enrich both interview AND decompose
- **Existing context pipeline:** `internal/engine/activities.go:387` (`buildCodebaseContextForTask`)
- **Duplicate context pipeline:** `internal/planning/activities.go:599` (same function, duplicated)
- **AST parser:** `internal/ast/parser.go` (`NewParser`, `ParseDir`)
- **Vector search:** `internal/ast/embed.go` (`EmbedFilter`, `FilterRelevantByEmbedding`)
- **Keyword fallback:** `internal/ast/filter.go` (`FilterRelevant`)
- **Lessons FTS5:** `internal/store/lessons.go` (`SearchLessons`)
- **Planning endpoints:** `internal/jarvis/plans_api.go` (`handlePlanInterview:144`, `handlePlanDecompose:290`)
- **API struct:** `internal/jarvis/api.go` (needs `AST` and `EmbedFilter` fields)
- **Engine workDirs:** `internal/engine/engine.go` (unexported `workDirs` map, needs accessor)
- **Related solution:** `docs/solutions/ui-bugs/chum-dashboard-plans-tab-document-rendering.md` — Plans tab rendering fixes

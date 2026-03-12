---
title: Planner Codebase Context Injection
date: 2026-03-12
status: complete
---

# Planner Codebase Context Injection

## What We're Building

Enrich the CHUM planner's interview endpoint (`handlePlanInterview`) with codebase context so the planning agent has awareness of project structure, code patterns, existing work, and past learnings. Currently it's a blind single-shot LLM call with zero codebase knowledge.

## Why This Matters

Planning is cheap relative to implementing the wrong thing. A planner that understands the codebase produces plans that reference real files, follow existing patterns, avoid duplicating work, and account for architectural constraints. The difference between "add authentication" and "add authentication using the existing SessionStore pattern in internal/auth/ with the middleware chain in cmd/server/middleware.go" is the difference between useful and not.

## Current State

`handlePlanInterview` in `internal/jarvis/plans_api.go`:
1. Reads PlanDoc from SQLite
2. Appends user message to conversation
3. Concatenates: system prompt + plan brief + conversation history
4. Calls `a.LLM.Plan(ctx, "claude", "claude-sonnet-4-20250514", ".", prompt)` — note hardcoded `"."` workDir
5. Parses structured JSON response, saves back to DB

**What it gets:** conversation history + plan document content
**What it doesn't get:** codebase, file tree, conventions, existing tasks, past solutions

## What Already Exists (Key Discovery)

The engine already has a complete codebase context pipeline for task execution:

- **AST parser** (`internal/ast/parser.go`) — tree-sitter Go parsing with mtime caching
- **Vector search** (`internal/ast/embed.go`) — Ollama embeddings + cosine similarity, threshold 0.35, max 30 relevant files
- **Keyword fallback** (`internal/ast/filter.go`) — tokenized keyword matching when embeddings unavailable
- **Lessons FTS5** (`internal/store/lessons.go`) — full-text search over past solutions/learnings
- **Integration** (`internal/engine/activities.go:387`) — `buildCodebaseContextForTask()` combines all of the above: relevant files get full source, surrounding files get signatures-only

The planner just doesn't use any of it.

## Architecture Bridge

`buildCodebaseContextForTask()` lives on `engine.Activities` (Temporal activity holder). `handlePlanInterview` lives on `jarvis.API`. These are different packages with different dependency graphs — you can't just call one from the other.

**Solution:** Extract the context-building logic into a standalone function (or new package) that both can use. The core logic only needs an `*ast.Parser`, an `*ast.EmbedFilter`, a workDir path, and a query string. Neither Temporal nor the API layer is a real dependency.

## Chosen Approach: Reuse + Extend Existing Pipeline

Extract the context-building from `engine.Activities`, make it a shared utility, and wire it into `handlePlanInterview` + `handlePlanDecompose`.

### Scope: Both Interview AND Decompose

`handlePlanDecompose` has the same blind-context problem — it calls `a.LLM.Plan(...)` with `"."` and no codebase context. Both handlers need enrichment. The decomposer benefits equally: with codebase context, it produces tasks that reference actual file paths and follow real patterns.

### Context Sources (in order of value)

1. **Fix workDir** — pass actual project workspace path instead of `"."`. Requires adding `WorkDir(project string) string` accessor to Engine (since `workDirs` map is unexported).

2. **AST + embeddings** — use the existing `ast.EmbedFilter` to find relevant files based on the conversation content (plan brief + user messages). Inject relevant file source + surrounding file signatures.

3. **DAG tasks/goals** — query `a.DAG.ListTasks(ctx, project, activeStatuses...)` for active goals and their children. Filter to active statuses (ready, running, etc.) — not the full history. The planner sees what work already exists, avoids duplication, can reference existing tasks.

4. **Lessons FTS5** — search `a.Store.SearchLessons(query, limit)` with keywords extracted from the plan brief + conversation. FTS5 requires sanitized queries (special chars escaped). Limit to ~5 results.

5. **CLAUDE.md** — read from project workspace via `os.ReadFile(filepath.Join(workDir, "CLAUDE.md"))` and inject directly. Note: fixing workDir (#1) lets the Claude CLI also read it natively, but explicit injection ensures it's in the prompt regardless of CLI behavior.

### Context Gathering Timing

**First turn + cache.** Gather context on the first user message (when topic keywords are available), cache in the PlanDoc (new field: `context_snapshot`). Subsequent turns reuse the cached context. User can trigger refresh via a "refresh context" message or button.

Rationale: codebase rarely changes during a 5-15 message planning session. First turn is when we know what the plan is about. Avoids per-turn latency.

### Token Compression

**Native Go compression** — no RTK dependency. Use the existing patterns:
- `truncateStr(s, n)` helper already exists in `dashboard_api.go`
- AST pipeline already does signatures-only for surrounding files
- Cap file tree depth, limit git log entries, truncate long descriptions
- Group files by directory for tree display

### Token Budget

Rough allocation per interview call:
- System prompt: ~600 tokens (existing)
- Conversation history: unbounded (existing, grows per turn)
- CLAUDE.md: ~500 tokens (new, truncated)
- Relevant code (full source, top files): ~2000 tokens (new)
- Surrounding code (signatures only): ~500 tokens (new)
- DAG active goals + tasks summary: ~300 tokens (new)
- Lessons matches: ~300 tokens (new)
- **Total new context: ~3600 tokens** — well within Sonnet's window

## Key Decisions

1. **Reuse existing AST + embedding pipeline** — don't build new context gathering, wire what exists
2. **Extract context-building into shared utility** — so both `engine.Activities` and `jarvis.API` can use it
3. **Native Go compression** — no RTK binary dependency
4. **First turn + cache** — gather once, cache in PlanDoc, refresh on demand
5. **Include DAG state** — active goals + tasks (filtered to active statuses) so planner avoids duplicating work
6. **Fix workDir immediately** — near-zero effort, lets Claude CLI read CLAUDE.md
7. **Enrich both interview AND decompose** — both handlers are blind today

## Open Questions

None — all resolved during brainstorming and review.

## Resolved Questions

- **Architecture bridge:** Extract context-building from `engine.Activities` into shared package (see Architecture Bridge section)
- **Which handlers to enrich:** Both `handlePlanInterview` and `handlePlanDecompose`
- **CLAUDE.md duplication:** Explicitly inject it into the prompt (don't rely on CLI workDir alone)
- **DAG task filtering:** Use active statuses only (ready, running, etc.), not full history
- **FTS5 keyword extraction:** Tokenize plan brief + user messages, sanitize for FTS5 syntax

## Out of Scope

- Tool use / agentic loop (separate future work)
- Streaming the interview response (currently blocking, separate concern)
- Changing the LLM from Sonnet to something else
- RTK integration (decided against, native Go compression)

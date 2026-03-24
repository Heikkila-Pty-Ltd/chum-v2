# Brainstorm: tmux-backed Planner Sessions

**Date:** 2026-03-13
**Status:** Draft

## What We're Building

Replace the synchronous LLM-over-HTTP pipeline in the Plans tab with interactive Claude CLI sessions managed via tmux. The dashboard becomes a thin UI layer over a real Claude Code session that can stream responses, use tools, read files, and interact with CHUM's DAG — all natively.

### The Problem Today

1. **Perceived latency**: `handlePlanInterview` shells out to `claude --print` (non-interactive, one-shot), blocks for 3-15 seconds, returns full response as JSON. User stares at a blinking cursor with no token feedback.
2. **Limited planner capability**: The planner can only generate text from a prompt. It can't read files, grep the codebase, or use tools during planning. All codebase context must be pre-gathered and injected into the prompt.
3. **Rigid structured output**: Every LLM response must conform to a JSON schema (`reply`, `next_question`, `structured`, `working_markdown`). This constrains natural conversation and forces complex prompt engineering.

### What Changes

- **Session lifecycle**: User clicks "+New Plan" → backend spawns a tmux session running `claude` with a system prompt seeded from CHUM context and planning skill prompts.
- **I/O bridge**: Dashboard sends user messages to the tmux session and streams output back to the browser. Tokens appear in real-time.
- **Tool access**: The Claude CLI session has access to CHUM CLI tools (exposed as commands or MCP tools) — query DAG, read/update tasks, update plan fields, trigger decompose.
- **Structured extraction**: Free-form conversation happens naturally. Structured plan data is extracted on-demand (user clicks "Extract Plan" or on plan status transition).

## Why This Approach

### Why not just add streaming to the current pipeline?

Streaming (Approach 1 in our discussion) fixes perceived latency but doesn't make the planner smarter. The planner still can't read files, grep code, or interact with the DAG during planning. It's just faster text generation.

### Why not the Anthropic API directly?

Building a custom agent runtime with tool definitions (Approach 3) duplicates work. We already have an agent runtime in `internal/engine/`. The Claude CLI already handles tool use, streaming, context management, and model selection. tmux lets us use all of that for free.

### Why tmux specifically?

- **Session management**: tmux provides named sessions, detach/reattach, and clean lifecycle management.
- **Output capture**: `tmux capture-pane` gives us the pane contents for streaming to the browser.
- **Process isolation**: Each plan gets its own session. Crashes don't affect other plans.
- **Existing pattern**: The codebase already spawns CLI subprocesses via `os/exec` (see `internal/llm/cli.go`). tmux is a natural extension.

## Key Decisions

### 1. Session spawned on "+New Plan"

When user creates a new plan, the backend:
1. Creates the PlanDoc in SQLite (existing flow)
2. Spawns `tmux new-session -d -s plan-{id}` running `claude` with:
   - System prompt seeded with planning context (adapted from `ce:plan` skill prompts)
   - CHUM CLI tools available (via pre-hook or MCP server)
   - Working directory set to the project's workspace

### 2. CHUM capabilities exposed as CLI tools

The planner Claude session gets full CHUM access via CLI tools injected through the pre-hook or as an MCP server:

| Tool | What it does |
|------|-------------|
| `chum-tasks list` | List tasks/goals from DAG, filterable by project/status |
| `chum-tasks get <id>` | Get task details (description, acceptance, status) |
| `chum-tasks create` | Create a new task in the DAG |
| `chum-tasks update <id>` | Update task fields |
| `chum-plan get <id>` | Get current plan document |
| `chum-plan update <id>` | Update plan fields (working_markdown, structured, etc.) |
| `chum-plan decompose <id>` | Trigger task decomposition |
| `chum-context build` | Gather codebase context (progressive disclosure format) |
| `chum-lessons search <query>` | Search lessons/solutions DB |

Delivered as a single `chum` CLI binary with subcommands, documented in the session's CLAUDE.md. MCP servers were considered but rejected — they're inconsistent and often missed by Claude. CLI tools via Bash are more reliable.

The CLI binary connects to the running dashboard via HTTP (`localhost:{port}`), wrapping existing `/api/dashboard/*` endpoints. No new backend code needed — the dashboard is always running when planner sessions exist. Direct SQLite was rejected due to concurrent write conflicts; Unix socket was rejected as more plumbing for marginal gain over HTTP.

### 3. Planning prompts from compound-engineering skills

The Claude session's system prompt incorporates planning methodology from `ce:plan`:
- Interview structure (one question at a time, progressive refinement)
- YAGNI principles
- Acceptance criteria patterns
- The structured output format (problem_statement, constraints, risks, etc.)

This isn't the rigid JSON schema — it's guidance in the system prompt that Claude follows naturally.

### 4. Session cleanup on plan status change

tmux sessions are destroyed when the plan transitions to `decomposed` or `approved`. The conversation phase is over — the plan has been extracted and structured.

Safety net: a daily reaper goroutine kills sessions for plans >24h old still in `needs_input` status.

### 5. Structured data extraction is on-demand

The conversation is free-form. When the user is satisfied:
- Click "Extract Plan" → runs a single pass over the conversation to extract structured fields
- Or: plan status transition triggers automatic extraction before cleanup

This decouples the conversation quality from the structured output requirement.

### 6. Dashboard UI bridges I/O

Pipe-based: spawn claude with stdout piped to a named pipe/file. Backend tails it and pushes chunks via SSE to the browser. User messages sent via writing to stdin pipe (avoids `tmux send-keys` escaping issues).

The frontend UI model (chat bubbles vs embedded terminal vs hybrid) is deferred to planning — the pipe-based backend design supports all options.

### 7. Conversation persistence

The I/O bridge intercepts messages as they flow and persists user/assistant turns to `PlanDoc.Conversation` in SQLite. Source of truth is the database — tmux is just the runtime. This enables search, audit, and survives tmux crashes.

## Existing Infrastructure to Leverage

| What exists | Where | How it helps |
|------------|-------|-------------|
| CLI subprocess spawning | `internal/llm/cli.go` | Pattern for `os/exec` with env filtering, workdir |
| Streaming + SSE | `internal/llm/stream.go`, `plan_handlers.go:handlePlanGroom` | SSE headers, chunk format, heartbeats |
| Codebase context builder | `internal/codebase/` | `Build()` + `FormatForPrompt()` for system prompt seeding |
| PlanDoc with status machine | `internal/dag/plan_store.go` | Status transitions trigger cleanup |
| Context caching | `context_snapshot` field on PlanDoc | First-turn context can seed the system prompt |
| Pre-hook pattern | `~/.picoclaw/bots/jarvis/scripts/prime.sh` | Proven pattern for injecting context into Claude sessions |

## Open Questions

*All resolved — see Resolved Questions.*

## Resolved Questions

1. **Core problem?** Both perceived latency AND planner capability. (Not just one.)
2. **UI model?** Deferred to planning — backend design is UI-agnostic. Open to chat bubbles, embedded terminal, or hybrid.
3. **Structured data?** Extract on-demand, not inline with every response.
4. **Session cleanup?** On plan status change (decomposed/approved), not idle timeout.
5. **Tool access scope?** Full CHUM access — read codebase, query DAG, create/update tasks, update plan fields, trigger decompose.
6. **Model choice?** Stays Sonnet — Haiku too limited for in-depth planning with structured output and codebase reasoning.
7. **I/O bridge?** Pipe-based. Spawn claude with stdout piped to a file/named pipe. Backend tails it and pushes chunks via SSE to the browser. Real-time streaming.
8. **Tool delivery?** Single `chum` CLI binary with subcommands, documented in session's CLAUDE.md. Connects to dashboard via HTTP (localhost). MCP servers rejected as inconsistent.
9. **Conversation persistence?** Yes, persist turns to SQLite as they flow through the I/O bridge. Source of truth is the database, tmux is just the runtime.
10. **Session reaper?** Yes, daily reaper goroutine kills tmux sessions for plans >24h old still in `needs_input` status.
11. **CLI transport?** HTTP to dashboard API. Dashboard is always running when sessions exist. Direct SQLite rejected (concurrent write conflicts). Unix socket rejected (marginal gain, more plumbing).
12. **Sub-agents?** Yes — Claude CLI can use its Task tool to spawn subagents for deep-dives. The tmux session is just the host process.
13. **File access?** Read + write to `docs/` only. Planner can create plan docs and brainstorms but cannot modify source code.
7. **I/O bridge?** Pipe-based. Spawn claude with stdout piped to a file/named pipe. Backend tails it and pushes chunks via SSE to the browser. Real-time streaming.
8. **Tool delivery?** CLI binary (`chum` with subcommands) documented in the session's CLAUDE.md. MCP servers are inconsistent and often missed by Claude — CLI via Bash tool is more reliable.
9. **Conversation persistence?** Yes, persist turns to SQLite as they flow through the I/O bridge. Source of truth is the database, tmux is just the runtime.
10. **Session reaper?** Yes, daily reaper goroutine kills tmux sessions for plans >24h old still in `needs_input` status.

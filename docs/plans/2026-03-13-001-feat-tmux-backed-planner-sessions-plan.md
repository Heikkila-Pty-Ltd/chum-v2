---
title: "feat: tmux-backed Interactive Planner Sessions"
type: feat
status: active
date: 2026-03-13
origin: docs/brainstorms/2026-03-13-tmux-backed-planner-sessions-brainstorm.md
deepened: 2026-03-13
---

# feat: tmux-backed Interactive Planner Sessions

## Enhancement Summary

**Deepened on:** 2026-03-13
**Sections enhanced:** 9
**Research agents used:** architecture-strategist, security-sentinel, performance-oracle, code-simplicity-reviewer, pattern-recognition-specialist, agent-native-reviewer, learnings-researcher, best-practices (tmux/SSE/CLI)

### Key Improvements

1. **Critical binary collision discovered**: `cmd/chum/main.go` already exists (704 lines, Temporal deps). New CLI must be a separate lightweight binary (`cmd/chum-planner-tools/main.go`)
2. **Security hardening**: Localhost API needs bearer token auth (DNS rebinding risk). Pipes must use private directory with 0700 permissions, not `/tmp`
3. **~45% complexity reduction possible**: 5-state machine → 3 states + busy mutex; kill-only reconciliation for v1; defer rate limiting, history replay, and chum CLI binary (use shell functions first)
4. **Goroutine leak prevention**: Context-aware channel sends are the #1 performance priority — prevents cascading pipe deadlocks
5. **Bridge responsibilities too broad**: Split into focused interfaces (reader, writer, persistence) following existing `stream.go` patterns
6. **Dual state machine anti-pattern**: Session state should be derived/transient, PlanDoc status is the single source of truth

### New Considerations Discovered

- Claude CLI supports `--input-format stream-json` + `--output-format stream-json` for bidirectional streaming — potential alternative to tmux+pipes worth spiking
- Turn detection is the riskiest technical decision — needs a dedicated spike before full implementation
- `internal/planner` name conflicts with existing `internal/planning` package — use `internal/plansession` instead
- Existing `AppendConversation` in `plan_store.go:268-306` already handles persistence with transaction safety and 500KB cap — reuse directly
- Named pipes have TOCTTOU problems; prefer `cmd.StdoutPipe()` unless tmux session persistence across backend restarts is required

---

## Overview

Replace the synchronous LLM-over-HTTP pipeline in the Plans tab with interactive Claude CLI sessions managed via tmux. The dashboard becomes a thin I/O bridge over a real Claude Code session that streams tokens in real-time, uses tools (file reads, DAG queries), and produces plans through natural conversation rather than rigid JSON schemas.

## Problem Statement / Motivation

The current planner (`handlePlanInterview` in `plans_backend.go:78`) has three compounding problems:

1. **3-15 second black hole**: Shells out to `claude --print` (one-shot, non-interactive), blocks until full response, returns JSON. User sees a blinking cursor with no token feedback.
2. **Blind planner**: The LLM can only generate text from a pre-assembled prompt. It cannot read files, grep code, query the DAG, or use tools during planning. All context must be gathered upfront via `codebase.Build()`.
3. **Rigid JSON contract**: Every response must conform to `{reply, next_question, structured, working_markdown}`. This constrains conversation quality and requires complex prompt engineering.

(see brainstorm: `docs/brainstorms/2026-03-13-tmux-backed-planner-sessions-brainstorm.md`)

## Proposed Solution

### Architecture

```
Browser ←──SSE──→ Go Backend ←──pipes──→ tmux session ──→ claude CLI
                      │                                       │
                      │ persist turns                         │ Bash tool
                      ↓                                       ↓
                   SQLite                              shell functions
                  (PlanDoc)                          (curl → dashboard API)
```

**Four new components:**

1. **Session Manager** (`internal/plansession/session.go`) — spawns/destroys tmux sessions, manages pipes, tracks session state
2. **I/O Bridge** (`internal/plansession/bridge.go`) — reads stdout pipe, writes stdin pipe, pushes chunks to SSE, persists turns to SQLite
3. **SSE Handler** (`internal/jarvis/plans_session.go`) — HTTP endpoints for session lifecycle and streaming
4. **Planning shell functions** (injected into tmux environment) — lightweight `curl` wrappers that call dashboard HTTP API

### Research Insights: Architecture

**Critical discovery — `cmd/chum/main.go` already exists:**
The existing `cmd/chum/main.go` is a 704-line binary with Temporal SDK, DAG, engine, and beads dependencies. It has `tasks`, `task create`, `plan`, `sync`, `shutdown`, `resume`, `reconcile`, `submit` subcommands that operate directly on the DAG. The plan's proposed lightweight HTTP-wrapper CLI **must not** collide with this binary. Two options:

- **Option A (recommended for v1):** Skip a separate binary entirely. Use shell functions (`chum-tasks`, `chum-plan`, etc.) injected into the tmux session via `.bashrc` or environment. Zero compilation overhead, instant iteration.
- **Option B:** Create `cmd/chum-planner-tools/main.go` — a separate lightweight binary with minimal dependencies (just `net/http`, `encoding/json`, `fmt`). Keeps the tmux-deployed binary small and fast.

**Package naming:**
Use `internal/plansession` (not `internal/planner`) to avoid confusion with the existing `internal/planning` package that handles Temporal workflow orchestration.

**Bridge decomposition:**
The bridge as described has 6 responsibilities (pipe reading, ANSI stripping, event dispatch, stdin writing, turn detection, conversation persistence). Split into focused interfaces:
- Pipe reader goroutine → reuse `stream.go` scanner+channel pattern
- Conversation persistence → call existing `DAG.AppendConversation` directly
- Turn detection → separate concern, easily swappable

**Dual state machine risk:**
The plan introduces a session state machine alongside the existing PlanDoc status machine. Make PlanDoc status the **single source of truth**. Session "state" (spawning/idle/busy) should be transient/in-memory only, derived from whether a tmux process exists and whether output is flowing.

**Alternative worth spiking:**
Claude CLI supports `--input-format stream-json` + `--output-format stream-json` for bidirectional streaming. This could replace tmux+pipes entirely — spawn `claude -p --input-format stream-json --output-format stream-json` as a long-running subprocess with direct stdin/stdout pipes via `os/exec`. Benefits: no tmux dependency, no named pipes, structured JSON events instead of heuristic turn detection. Downside: loses tmux session persistence across backend restarts.

### Session State Machine

```
           +New Plan
               │
               ▼
          ┌──────────┐
          │ starting  │──── spawn fails ───→ [error returned to browser]
          └────┬──────┘
               │ claude ready
               ▼
          ┌──────────┐    user message    ┌──────┐
          │  ready    │──────────────────→│ ready │ (busy mutex held)
          └────┬──────┘←────────────────── └──────┘
               │         turn complete (mutex released)
               │
               ├── Extract Plan ──→ [inject extraction prompt, parse output, save to PlanDoc]
               │
               ├── status → decomposed/approved ──→ [pipes closed, tmux killed]
               │
               └── claude crashes / pipe EOF ──→ [user sees error, offered restart]
```

**Simplification from 5 states to 3:** `starting`, `ready`, `done` — plus a `sync.Mutex` for the busy guard. No separate `idle`/`busy`/`teardown`/`failed` states. The mutex on `ready` prevents concurrent stdin writes. `done` covers both clean teardown and failure (with an error field).

**Concurrency rule**: When the busy mutex is held, message submissions are rejected with HTTP 409 ("Claude is currently responding"). Frontend shows a disabled input with activity indicator. This avoids stdin write interleaving entirely.
(Addresses SpecFlow critical question #4)

### Research Insights: State Machine Simplification

**Before (5 states):**
```
spawning → idle ⇄ busy → teardown
                       ↘ failed
```

**After (3 states + mutex):**
```
starting → ready (mutex guards busy) → done
```

**Why simpler:** The `idle`/`busy` distinction is really just "is the mutex held?" — not a state transition. `teardown` and `failed` are both "done with optional error." This eliminates 4 transition edges and the associated state validation code.

**Estimated impact:** ~200 LOC reduction in session management, eliminates the state transition validation function entirely.

### Session Registry Persistence

The session registry is in-memory but reconciled on startup:

```go
// On backend start (kill-only for v1):
// 1. Query tmux ls for active plan-* sessions
// 2. Kill ALL orphaned tmux sessions
// 3. Plans that were mid-session get marked as needing restart
//    (user clicks to re-start, which is simpler than re-adoption)
```

### Research Insights: Reconciliation

**Simplification:** Full re-adoption (re-attach stdin/stdout pipes to a running tmux session) is complex and error-prone for v1. Kill-only reconciliation is sufficient:

1. On startup, kill all `plan-*` tmux sessions
2. Plans in `needs_input` status show a "Session expired — click to restart" message
3. Conversation history is preserved in SQLite, so restart resumes context

**Why this is enough:** Backend restarts are rare in production. The conversation is persisted. The user loses only the in-flight response, which they can re-request. Re-adoption adds ~300 LOC of pipe re-wiring with subtle failure modes for a scenario that happens maybe once a week.

## Technical Approach

### Phase 1: Session Manager + I/O Bridge (Foundation)

Build the core infrastructure that spawns tmux sessions and bridges I/O.

#### 1.1 Session Manager — `internal/plansession/session.go`

- [x] Define `Session` struct: `ID`, `PlanID`, `State` (starting/ready/done), `StdinPipe`, `StdoutPipe`, `BusyMu sync.Mutex`, `Err error`, `CreatedAt`
- [x] Define `Manager` struct with mutex-guarded session map (not `sync.Once` — sessions can fail transiently; use mutex+nil-check pattern per learnings from `expose-jarvis-kb-actions` solution)
- [x] `Spawn(planID, project, workDir) (*Session, error)` — creates tmux session:
  ```
  tmux new-session -d -s plan-{planID} \
    -x 200 -y 50 \
    "claude --model claude-sonnet-4-20250514 \
            --allowedTools 'Bash(chum-* *),Bash(cat *),Bash(ls *),Bash(grep *),Bash(find *)' \
            --permission-mode plan \
            -p /path/to/prompt.md"
  ```
  Note: exact flags TBD based on claude CLI capabilities. `--permission-mode plan` may not exist — fallback is `--print` with custom allowed tools.
- [x] Pipe setup: Use private directory `~/.chum/pipes/{planID}/` with `0700` permissions. Create `stdin.pipe` and `stdout.pipe` FIFOs there.
  - Alternative: use `cmd.StdoutPipe()` directly without tmux (simpler but loses tmux session management)
  - **Spike first:** Try `claude -p --input-format stream-json --output-format stream-json` with direct `os/exec` pipes — if this works, skip tmux entirely for v1
- [x] `Destroy(planID) error` — separate persistence from side effects (per learnings):
  1. Update session state to `done` in registry (pure state)
  2. Send `tmux kill-session -t plan-{planID}` (side effect, best-effort)
  3. Clean up pipes/temp files
- [x] `Reconcile()` — called on backend startup: kill all `plan-*` tmux sessions (kill-only for v1)
- [x] Concurrency gate: hard-code max 3 concurrent sessions for v1 (configurable later if needed)

#### Research Insights: Session Spawning

**tmux best practices (from research):**
- Use thin wrapper over `os/exec`, not a tmux library — no Go tmux libraries are mature enough
- `tmux wait-for -S plan-{id}-ready` provides clean process-exit signaling without polling
- Set `TERM=xterm-256color` for consistent ANSI output
- Use `-x 200 -y 50` to avoid line-wrapping issues in capture-pane

**Pipe security (from security review):**
- **CRITICAL:** Do NOT use `/tmp/chum-planner-*` — world-readable, symlink attack vector
- Use `~/.chum/pipes/{planID}/` with `os.MkdirAll(dir, 0700)`
- Clean up pipes in `Destroy()` with `os.RemoveAll(dir)`

**Concurrency (from simplicity review):**
- Hard-code max 3 sessions for v1 instead of a configurable semaphore
- This is a single-user system — 3 concurrent planning sessions is already generous
- If needed later, `golang.org/x/sync/semaphore.Weighted` is the right abstraction

**Sentinel errors (from pattern review):**
Define typed errors for programmatic handling:
```go
var (
    ErrSessionNotFound = errors.New("session not found")
    ErrSessionBusy     = errors.New("session is busy")
    ErrSpawnFailed     = errors.New("session spawn failed")
)
```

#### 1.2 I/O Bridge — `internal/plansession/bridge.go`

- [x] `Bridge` struct: wraps a `Session`, owns the stdout reader goroutine
- [x] Stdout reader goroutine:
  - Reads from stdout pipe line by line
  - Strips ANSI escape codes (compile regex ONCE at package level: `var ansiRe = regexp.MustCompile(...)`)
  - Sends chunks to a `chan BridgeEvent` (buffered, size 256)
  - **Context-aware sends**: Use `select { case ch <- event: case <-ctx.Done(): return }` — prevents goroutine leak when SSE client disconnects
  - On pipe EOF: sends `BridgeEvent{Type: EventSessionFailed}`, marks session as `done`
  - On context cancellation: clean shutdown
- [x] Stdin writer:
  - `SendMessage(msg string) error` — acquires busy mutex, writes message + newline to stdin pipe, releases on turn completion
  - Returns `ErrSessionBusy` if mutex is already held
- [x] Turn detection: heuristic-based (riskiest part — spike first)
  - Primary: quiescence timeout (2-3s no output after generation started)
  - Enhanced: scan last 5 lines for prompt regex pattern (e.g., `^\$ $` or claude's prompt marker)
  - **Spike this before implementing the rest of the bridge**
- [x] Conversation persistence: call existing `DAG.AppendConversation` (plan_store.go:268-306) — already has transaction safety and 500KB cap
- [x] `BridgeEvent` types: use Go constants (like `internal/planning/types.go:77-88`):
  ```go
  const (
      EventToken           = "token"
      EventTurnComplete    = "turn_complete"
      EventToolUse         = "tool_use"
      EventError           = "error"
      EventSessionDestroyed = "session_destroyed"
  )
  ```

#### Research Insights: I/O Bridge

**Goroutine leak prevention (P0 from performance review):**
The #1 performance risk is goroutine leaks when SSE clients disconnect. The stdout reader goroutine must use context-aware channel sends:
```go
select {
case ch <- event:
case <-ctx.Done():
    return // Don't block on a dead channel
}
```
Without this, a disconnected SSE client leaves the goroutine blocked on `ch <-`, which prevents the pipe from draining, which blocks the tmux process, which causes the session to hang.

**ANSI regex (from performance review):**
Compile once at package level, not per-line:
```go
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
```

**Scanner buffer (from performance review):**
Set explicit buffer size for `bufio.Scanner` — default 64KB may truncate long tool-use outputs:
```go
scanner := bufio.NewScanner(pipe)
scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB
```

**Reuse existing patterns (from pattern review):**
- The channel-based event dispatch mirrors `stream.go:74` (`chan StreamChunk`, buffered size 64). Plan's larger buffer (256) is appropriate for potentially longer tmux output.
- `mustJSON` helper already exists in `plan_handlers.go:317-323` — reuse directly.
- `filterEnv()` in `cli.go` should be reused for environment setup.

**Bridge goroutine ownership (from pattern review):**
Follow `stream.go` pattern: the function that creates the goroutine owns the channel and closes it via `defer close(ch)`. The channel is the sole return value. Don't leak ownership.

**Pipe EOF as cleanup signal (from performance review):**
When the pipe hits EOF, trigger immediate cleanup — don't wait for the hourly reaper. The pipe EOF means claude has exited, so the session is already dead.

#### 1.3 Reaper — `internal/plansession/reaper.go`

- [x] One-shot cleanup on backend startup (kill orphaned tmux sessions) — NOT a periodic goroutine for v1
- [x] Pipe EOF already triggers immediate cleanup for normal operation
- [ ] If periodic reaper is needed later, run every hour and kill sessions for plans >24h in `needs_input`

#### Research Insights: Reaper

**Simplification (from simplicity review):**
For v1, the reaper is unnecessary if cleanup works correctly:
- Normal end: plan status change triggers `Destroy()`
- Crash: pipe EOF triggers immediate cleanup
- Backend restart: `Reconcile()` kills all orphaned sessions

A periodic reaper is defense-in-depth for bugs in the above. Defer to v2 unless testing reveals leaks.

#### 1.4 Tests

- [x] `session_test.go`: mock tmux commands, verify spawn/destroy lifecycle
- [ ] `bridge_test.go`: pipe a known sequence, verify event stream and turn detection
- [x] Run: `go test ./internal/plansession/...`

### Phase 2: HTTP Handlers + Frontend Integration

Wire the session manager into the dashboard API and update the Plans tab UI.

#### 2.1 SSE Handler — `internal/jarvis/plans_session.go`

New endpoints (follow existing route pattern in `api.go`):

- [x] `POST /api/dashboard/plan/{id}/session` — spawn a session for this plan
  - Creates PlanDoc if needed, spawns session, returns `{session_id, status}`
  - Idempotent: if session already exists and is healthy, return it
  - Error if plan is in terminal status (decomposed/approved/materialized)

- [x] `GET /api/dashboard/plan/{id}/session/stream` — SSE endpoint
  - Extract SSE headers into shared helper: `func setSSEHeaders(w http.ResponseWriter)` (currently duplicated in `handlePlanGroom`)
  - Follows exact SSE pattern from `handlePlanGroom` (`plan_handlers.go:144-276`)
  - Reads from `Bridge.Events()` channel
  - Heartbeat every 15s
  - **Bearer token validation**: Check `Authorization: Bearer {token}` header (token set via `CHUM_SESSION_TOKEN` env var)
  - On reconnect: sends recent conversation history from SQLite as `event: history` before live streaming
  - SSE event schema:
    ```
    event: token
    data: {"text": "..."}

    event: turn_complete
    data: {"turn_id": "..."}

    event: tool_use
    data: {"tool": "chum-tasks list", "status": "running"}

    event: error
    data: {"message": "...", "recoverable": true}

    event: session_destroyed
    data: {"reason": "status_change"}

    event: heartbeat
    data: {}

    event: history
    data: {"turns": [...]}
    ```
  - **Do NOT name any event "error"** — it collides with EventSource's built-in error event. Use `event: session_error` instead.

- [x] `POST /api/dashboard/plan/{id}/session/message` — send message to session
  - Writes to stdin pipe via bridge
  - Returns `202 Accepted` (response comes via SSE, not this endpoint)
  - Returns `409 Conflict` if session busy mutex is held
  - Returns `404` if no active session
  - Persists user turn to SQLite immediately via `AppendConversation`

- [x] `POST /api/dashboard/plan/{id}/session/extract` — extract structured plan
  - Injects extraction prompt into session via SSE (async, not blocking HTTP)
  - Returns `202 Accepted` — extraction result arrives via SSE `event: extraction_complete`
  - Frontend shows extraction progress in real-time

- [x] `DELETE /api/dashboard/plan/{id}/session` — manually destroy session
  - Calls `Manager.Destroy(planID)`

- [x] Wire into `api.go`: add `PlanSession *plansession.Manager` field to `API` struct, gate routes on `a.PlanSession != nil`
- [x] CLAUDE.md warns about handler file redeclarations — document which functions live in `plans_session.go` vs `plan_handlers.go` vs `plans_backend.go`

#### Research Insights: HTTP Handlers

**SSE best practices (from research):**
- Set `X-Accel-Buffering: no` for nginx compatibility
- Use `Last-Event-ID` header for reconnection — assign incrementing IDs to events
- For multi-tab support: use a Broker pattern with per-client channels. On subscription overflow, drop oldest events (backpressure) rather than blocking the bridge goroutine.
- Graceful shutdown: use a separate `context.Context` for SSE handlers so server shutdown can drain connections.

**Security (from security review):**
- **CRITICAL-01: Unauthenticated localhost API** — DNS rebinding attacks can reach localhost. Add bearer token auth: generate a random token on startup, pass to tmux sessions via env var `CHUM_SESSION_TOKEN`, validate on every API call from the CLI.
- Session IDs should not be guessable — plan correctly uses UUIDs from SQLite.
- Rate limiting the message endpoint is unnecessary for a single-user system (simplicity review).

**Event naming (from SSE research):**
- Don't use `event: error` — it collides with EventSource's built-in `onerror` handler. Use `event: session_error`.
- Don't name events with special characters — stick to alphanumeric + underscore.

**Extract endpoint (from architecture review):**
The blocking HTTP extract endpoint (`POST /extract` → wait for response → return) is risky. If extraction takes >30s (Claude needs to read the full conversation and produce structured output), the HTTP connection may time out. Make it async: inject the extraction prompt, stream the result via SSE, save to PlanDoc on completion.

**Transport contract (from institutional learnings — Pattern 1):**
When the SSE handler and message endpoint coexist, ensure the frontend correctly handles:
- `202 Accepted` from message send (not the response itself)
- Response tokens via SSE (not the HTTP response body)
This is the exact class of SSE/JSON contract mismatch that caused blank renders in PR #76.

#### 2.2 Frontend — `web/views/plans.js`

- [x] Replace `sendMessage()` (line 657-709) with SSE-based flow:
  - On plan open: `POST /session` to ensure session exists, then connect `EventSource` to `/session/stream`
  - On message send: `POST /session/message`, show message immediately in chat, disable input (busy state)
  - On `event: token`: append text to streaming bubble (reuse `appendStreamingBubble()`)
  - On `event: turn_complete`: finalize bubble, re-enable input
  - On `event: session_error`: show error in chat, re-enable input if recoverable
  - On `event: session_destroyed`: show "Session ended" message, disable chat
  - On `EventSource` disconnect: reconnect with exponential backoff, expect `event: history` on reconnect
  - **Reset EventSource on plan/project switch** (institutional learning from PR #76 fixes)

- [x] Add "Extract Plan" button to the Plan tab header
  - Click: `POST /session/extract`, show streaming extraction progress, render result in document pane

- [x] Update state machine: `IDLE` → `SENDING` → `STREAMING` → `IDLE` stays the same but driven by SSE events not fetch response

- [x] Handle `409 Conflict` on message send: show "Claude is responding..." toast, don't clear input

- [x] Treat SSE stream as optional (per institutional learning) — use `Promise.allSettled` if mixing session setup with other API calls

#### Research Insights: Frontend

**Transport contract validation (from institutional learnings):**
Three critical patterns from PR #76 apply directly:
1. SSE parsers silently produce empty results on JSON responses — never mix formats
2. Reset EventSource on project/plan switch — stale connections cause ghost events
3. Validate frontend field access against Go struct definitions — `DraftTask.type` doesn't exist

**EventSource reconnection (from SSE research):**
```javascript
function connectSSE(planId) {
  const es = new EventSource(`/api/dashboard/plan/${planId}/session/stream`);
  es.addEventListener('token', (e) => { /* append text */ });
  es.addEventListener('turn_complete', (e) => { /* re-enable input */ });
  es.addEventListener('session_error', (e) => { /* show error */ });

  // Handle reconnection — EventSource auto-reconnects on network errors
  // but CLOSED readyState requires manual re-creation
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED) {
      setTimeout(() => connectSSE(planId), 1000);
    }
  };
}
```

#### 2.3 Tests

- [ ] Integration test: spawn session, send message via HTTP, verify SSE events
- [ ] Frontend: manual testing (no JS test framework in place)
- [ ] Run: `go test ./internal/jarvis/... && go test ./internal/plansession/...`

### Phase 3: Planning Tools for Claude

Give the claude CLI session access to CHUM capabilities.

#### 3.1 V1: Shell Functions (NOT a compiled binary)

- [x] Generate a shell script `~/.chum/sessions/{planID}/tools.sh` that defines functions:
  ```bash
  CHUM_API="http://localhost:${CHUM_API_PORT}"
  CHUM_TOKEN="${CHUM_SESSION_TOKEN}"

  chum-tasks() {
    curl -s -H "Authorization: Bearer $CHUM_TOKEN" \
      "$CHUM_API/api/dashboard/tasks?project=$1&status=$2" | jq -r '.[] | "\(.id)\t\(.title)\t\(.status)"'
  }

  chum-plan() {
    curl -s -H "Authorization: Bearer $CHUM_TOKEN" \
      "$CHUM_API/api/dashboard/plan/$1" | jq .
  }

  chum-context() {
    curl -s -H "Authorization: Bearer $CHUM_TOKEN" \
      "$CHUM_API/api/dashboard/context/build?project=$1&query=$2"
  }

  chum-lessons() {
    curl -s -H "Authorization: Bearer $CHUM_TOKEN" \
      "$CHUM_API/api/dashboard/lessons/search?q=$1"
  }
  ```
- [x] Source this script in the tmux session's shell initialization
- [x] `CHUM_API_PORT` and `CHUM_SESSION_TOKEN` set as env vars by session manager

#### Research Insights: Tool Delivery

**Shell functions vs compiled binary (from simplicity review):**
A compiled Go binary (`cmd/chum-planner-tools/main.go`) is over-engineering for v1. Shell functions wrapping `curl` are:
- Zero compilation overhead
- Instantly iterable (edit the script, no rebuild)
- Trivially debuggable (`set -x` to see the curl calls)
- Sufficient for the expected usage pattern (a few calls per planning session)

Graduate to a compiled binary only if: shell function parsing becomes unreliable for Claude, or performance matters (unlikely — these are infrequent HTTP calls).

**Binary name collision (from architecture + pattern reviews):**
The existing `cmd/chum/main.go` has 704 lines with Temporal, DAG, engine, and beads dependencies. Adding HTTP-client subcommands would bloat this binary with conflicting concerns. If a compiled binary is needed later, use `cmd/chum-planner-tools/main.go` — minimal deps (`net/http`, `encoding/json`, `fmt`, `text/tabwriter`).

**Agent-native gaps (from agent-native review):**
For full CRUD parity, the shell functions should eventually support:
- `chum-tasks create --title T --description D --parent P`
- `chum-tasks update <id> --field value`
- `chum-tasks delete <id>` (with confirmation)
- `chum-plan update <id> --field value`
- `chum-plan decompose <id>`

For v1, read-only access (`list`, `get`, `search`) is sufficient. Add write operations as needed.

#### 3.2 Missing API endpoints

Audit existing endpoints and add any missing ones needed by the tools:

- [ ] `GET /api/dashboard/lessons/search?q=...` — search lessons/solutions FTS5 (may not exist yet)
- [ ] `GET /api/dashboard/context/build?project=...&query=...` — expose `codebase.Build` + `FormatForPrompt` as an endpoint (may not exist yet)
- [ ] Add bearer token validation middleware for all session-related endpoints
- [ ] Verify existing task/plan endpoints cover all tool functions

#### 3.3 Session CLAUDE.md

- [x] Generate a per-session CLAUDE.md that's placed in the working directory (or injected via system prompt):
  ```markdown
  # Planning Session

  You are a planning assistant for the CHUM project management system.
  You are interviewing a human to refine a plan into well-specified tasks.

  ## Available Tools

  Use the shell functions to interact with the project:

  ```bash
  chum-tasks myproject          # List tasks
  chum-plan PLAN-456            # Get current plan
  chum-context myproject "auth" # Get codebase context
  chum-lessons "transport"      # Search past solutions
  ```

  ## Planning Methodology

  - Ask ONE focused question at a time
  - Build on previous answers
  - Use `chum-context` to ground your understanding in actual code
  - Use `chum-lessons` to check for past solutions
  - Validate assumptions against the codebase before recommending
  - Apply YAGNI — prefer simpler approaches

  ## File Access

  You may read any file in the project. You may write ONLY to docs/ directory.

  ## Structured Output

  When asked to extract a plan, produce a JSON object with:
  - problem_statement, desired_outcome, summary
  - constraints, assumptions, non_goals, risks
  - open_questions, validation_strategy
  - working_markdown (full plan document in markdown)
  ```

#### 3.4 Tests

- [ ] Integration: start dashboard, run shell functions, verify output
- [ ] Run: `go test ./internal/plansession/...`

### Phase 4: Integration + Cleanup

Wire everything together and remove/deprecate old code paths.

- [x] Update `cmd/dashboard-preview/main.go` to create `plansession.NewManager()` and wire into `jarvis.API`
- [x] Source tools.sh during session spawn
- [x] Deprecate (don't remove yet) `handlePlanInterview` — keep as fallback if tmux is unavailable
- [ ] Update `CLAUDE.md` with new architecture notes
- [x] Extract shared SSE helper: `func setSSEHeaders(w http.ResponseWriter)` — reused by both `handlePlanGroom` and new session stream handler
- [x] Run full test suite: `go test ./... && go build ./...`

## Alternative Approaches Considered

| Approach | Why rejected |
|----------|-------------|
| **Just add streaming to current pipeline** | Fixes latency but planner still can't use tools. Doesn't address the capability gap. (see brainstorm) |
| **Direct Anthropic API** | Duplicates the agent runtime we already have via Claude CLI. More code to maintain. (see brainstorm) |
| **MCP server for tool delivery** | MCP servers are inconsistent and often missed by Claude. CLI via Bash tool is more reliable. (see brainstorm, resolved question #8) |
| **Direct SQLite for CLI transport** | Concurrent write conflicts with dashboard process. (see brainstorm, resolved question #11) |
| **Compiled CLI binary for v1** | Over-engineering. Shell functions wrapping curl are simpler, instantly iterable, and sufficient for the expected usage pattern. Graduate to compiled binary if needed. (simplicity review) |
| **Configurable session semaphore** | YAGNI for single-user. Hard-code max 3. (simplicity review) |
| **Re-adoption on backend restart** | Too complex for v1 (~300 LOC of pipe re-wiring). Kill-only reconciliation is sufficient. (simplicity + architecture reviews) |

## System-Wide Impact

- **Interaction graph**: `+New Plan` → `Session.Spawn()` → `tmux new-session` → `claude` CLI process. Message send → stdin pipe write → claude processes → stdout pipe → bridge goroutine → SSE handler → browser. Plan status change → `Session.Destroy()` → `tmux kill-session`.
- **Error propagation**: Pipe EOF → bridge detects → sends `session_error` event → SSE → browser shows error. HTTP failures in shell functions → non-zero exit → claude sees error in Bash tool result → can retry or inform user.
- **State lifecycle risks**: Session registry is in-memory. Backend restart loses registry but kills all tmux sessions (kill-only reconciliation). SQLite conversation persistence survives all crashes. No dual-state-machine synchronization needed — PlanDoc status is the single source of truth.
- **API surface parity**: New session endpoints coexist with old interview endpoints. Both paths update the same PlanDoc. No breaking changes.

### Research Insights: System-Wide Impact

**Error propagation (from architecture review):**
- Define which errors are recoverable (`recoverable: true`) — network timeouts, transient tmux failures
- Define which are fatal (`recoverable: false`) — pipe EOF, tmux process exit, plan in terminal status
- Frontend should auto-retry recoverable errors (1 attempt), show error + restart button for fatal

**State lifecycle (from pattern review):**
- Two independent state machines (session registry + PlanDoc status) is a classic anti-pattern
- Resolution: PlanDoc status is authoritative. Session state is transient/derived:
  - PlanDoc `needs_input` + tmux alive = session `ready`
  - PlanDoc `decomposed` = session should not exist
  - No PlanDoc status for "session spawning" — use a transient flag

## Acceptance Criteria

### Functional Requirements

- [ ] User clicks "+New Plan" → tmux session spawns → tokens stream to browser within 1s
- [ ] User sends message → tokens appear incrementally via SSE (no 3-15s black hole)
- [x] Claude can use shell functions to query tasks, plans, codebase context, and lessons
- [x] Claude can read any project file and write to `docs/` only
- [x] "Extract Plan" produces structured PlanDoc fields from conversation (async via SSE)
- [ ] Plan status change to decomposed/approved destroys tmux session
- [x] Backend restart kills all orphaned tmux sessions (kill-only reconciliation)
- [x] Message rejected with 409 while Claude is responding (busy mutex)
- [x] Browser reconnect receives conversation history then live stream
- [x] Conversation turns persisted to SQLite via existing `AppendConversation`
- [ ] API endpoints require bearer token authentication

### Non-Functional Requirements

- [ ] First token latency < 1s from message send (vs current 3-15s)
- [x] Max concurrent sessions: 3 (hard-coded for v1)
- [x] `go build ./...` passes with all new code
- [x] `go test ./...` passes with new tests
- [x] `gofmt` clean
- [x] No goroutine leaks — context-aware channel sends in bridge
- [x] Pipes in private directory with 0700 permissions

## Dependencies & Risks

| Risk | Mitigation |
|------|-----------|
| Claude CLI pipe mode may not support clean stdin/stdout separation | **Spike first**: try `--input-format stream-json` with direct `os/exec` pipes. Fallback: `tmux send-keys` + `tmux capture-pane` polling |
| Turn detection heuristic may be unreliable | **Spike first**: test quiescence timeout + prompt regex with real claude output. This is the riskiest technical decision. |
| ANSI stripping may miss edge cases | Use battle-tested regex; compile once at package level; test with real claude output |
| Claude ignores "docs/ only" write constraint | Prompt-level constraint only. Acceptable trust decision for now — user is running this on their own server. Document as known limitation |
| `cmd/chum/main.go` already exists | Use shell functions for v1. If compiled binary needed later, use `cmd/chum-planner-tools/main.go` |
| DNS rebinding attacks on localhost API | Bearer token auth on all session endpoints. Token generated on startup, passed via env var |
| Named pipe TOCTTOU / blocking | Prefer `cmd.StdoutPipe()` if possible. If named pipes needed, use private directory with 0700 |
| `internal/planner` name collision with `internal/planning` | Use `internal/plansession` |
| Bridge goroutine leak on SSE client disconnect | Context-aware `select` on all channel sends |
| SQLite write contention from shell functions | Already mitigated — shell functions use HTTP API, dashboard handles SQLite writes. Enable WAL mode for read concurrency. |

## Security Considerations

- **Authentication**: Bearer token required on all session-related API endpoints. Token generated randomly on backend startup, passed to tmux sessions via `CHUM_SESSION_TOKEN` env var. Mitigates DNS rebinding and SSRF attacks on localhost.
- **Pipe isolation**: Session pipes stored in `~/.chum/pipes/{planID}/` with `0700` permissions. Not in `/tmp` (world-readable, symlink attack vector).
- **Sandbox**: Claude's file write constraint is prompt-level, not OS-enforced. Acceptable for single-user self-hosted deployment. If multi-user is needed later, add filesystem namespaces.
- **Session IDs**: Use plan IDs (UUIDs from SQLite) as session identifiers. Not guessable.
- **tmux socket**: Restrict tmux socket permissions to prevent other local users from attaching to sessions.
- **Command injection**: Shell functions use positional arguments, not string interpolation. The `--allowedTools` pattern restricts which Bash commands Claude can run.

### Research Insights: Security

**From security review (2 critical, 3 high findings):**

- **CRITICAL-01: Unauthenticated localhost API** — Without auth, DNS rebinding attacks can proxy requests to localhost. A malicious webpage could craft DNS entries resolving to 127.0.0.1, then POST to `/api/dashboard/plan/{id}/session/message`. Fix: bearer token on all session endpoints.
- **CRITICAL-02: World-accessible pipes in /tmp** — Named pipes in `/tmp/chum-planner-*` are readable by any local user. Symlink attack: attacker creates `/tmp/chum-planner-{id}.out` → `/etc/passwd` before the session starts. Fix: private directory with 0700.
- **HIGH-01: Command injection** — If shell functions interpolate user input into curl URLs without escaping, Claude could craft inputs that break out of the curl command. Fix: use positional arguments, URL-encode parameters.
- **HIGH-02: Write access is policy-only** — Claude's "docs/ only" restriction is a prompt instruction, not enforced by the OS. Acceptable for single-user, but document as a known limitation.
- **HIGH-03: Session ID via tmux ls** — Any local user can run `tmux ls` to see session names containing plan IDs. Fix: restrict tmux socket permissions.

## Sources & References

### Origin

- **Brainstorm document:** [docs/brainstorms/2026-03-13-tmux-backed-planner-sessions-brainstorm.md](docs/brainstorms/2026-03-13-tmux-backed-planner-sessions-brainstorm.md)
  - Key decisions: tmux per plan, pipe-based I/O, CLI binary over MCP, HTTP transport, docs/-only writes, on-demand extraction

### Internal References

- SSE handler pattern: `internal/jarvis/plan_handlers.go:144-276` (`handlePlanGroom`)
- Streaming infrastructure: `internal/llm/stream.go:32-121` (`RunCLIStream`, `StreamChunk`)
- CLI subprocess pattern: `internal/llm/cli.go:56-121` (`RunCLI`, `BuildPlanCommand`)
- PlanDoc status machine: `internal/dag/plan_store.go:232-255` (`TransitionPlanStatus`)
- Conversation persistence: `internal/dag/plan_store.go:268-306` (`AppendConversation`)
- Binary wiring pattern: `cmd/dashboard-preview/main.go:23-127`
- Codebase context builder: `internal/codebase/context.go`, `format.go`
- Existing chum binary: `cmd/chum/main.go` (704 lines — do NOT collide)
- Phase constants pattern: `internal/planning/types.go:77-88`
- Environment filtering: `internal/llm/cli.go` (`filterEnv`)

### Institutional Learnings Applied

- **Mutex+nil-check for fallible init** (not `sync.Once`): `docs/solutions/integration-issues/expose-jarvis-kb-actions-through-retryable-dashboard-system-20260312.md`
- **Separate persistence from side effects**: `docs/solutions/integration-issues/harden-task-close-and-github-cli-coordination-system-20260312.md`
- **Transport contract validation (SSE vs JSON)**: `docs/solutions/patterns/critical-patterns.md` Pattern 1
- **Progressive disclosure for LLM context**: `docs/solutions/patterns/critical-patterns.md` Pattern 3
- **Validate frontend against backend structs**: `docs/solutions/patterns/critical-patterns.md` Pattern 4
- **Promise.allSettled for optional calls**: `docs/solutions/patterns/critical-patterns.md` Pattern 6
- **Reset EventSource on project switch**: `docs/solutions/ui-bugs/dashboard-pr-review-fixes-batch.md`
- **Process-wide rate limiting for subprocesses**: `docs/solutions/integration-issues/harden-task-close-and-github-cli-coordination-system-20260312.md`
- **Codebase context injection**: `docs/solutions/integration-issues/planner-codebase-context-injection.md`

### External References

- Claude CLI reference: `claude --help` — key flags: `--input-format stream-json`, `--output-format stream-json`, `--allowedTools`, `--permission-mode`, `--system-prompt`
- tmux man page: `tmux(1)` — `new-session`, `kill-session`, `wait-for`, `pipe-pane`, `capture-pane`
- SSE specification: W3C Server-Sent Events
- Go `x/sync/semaphore`: `golang.org/x/sync/semaphore` (for future configurable concurrency)

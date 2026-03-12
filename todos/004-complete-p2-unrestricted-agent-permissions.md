---
status: complete
priority: p2
issue_id: "004"
tags: [code-review, security, agent-execution]
dependencies: []
---

# LLM Agents Run with Unrestricted Permissions — No Sandboxing

## Problem Statement

All LLM agents (Claude, Gemini, Codex) run with maximally permissive flags: `--dangerously-skip-permissions`, `--approval-mode yolo`, `--full-auto`. Combined with prompt injection vectors (task descriptions from beads, review feedback from GitHub comments), an attacker can achieve arbitrary code execution on the worker host.

## Findings

- `internal/llm/cli.go:143-163` — flags: `--dangerously-skip-permissions` (claude), `--approval-mode yolo --sandbox false` (gemini), `--full-auto` (codex)
- Agents can read any file, execute any command, make network requests
- Worktree isolation only scopes git operations, not process access
- Review feedback injected directly into agent prompt without sanitization (`agent.go:508-513`)
- Task descriptions from beads/DAG flow into agent prompts unsanitized (`activities.go:134-142`)

## Proposed Solutions

### Option A: Container sandboxing (Recommended for production)
Run each agent in a Docker/nsjail container with: mounted worktree only, no network (except git push), dropped capabilities, resource limits.

**Pros:** Strong isolation, blast radius containment
**Cons:** Significant infrastructure change, Docker dependency
**Effort:** Large
**Risk:** Medium

### Option B: Sanitize prompt inputs
Strip or escape potentially dangerous content from task descriptions and review feedback before injecting into agent prompts.

**Pros:** Lower effort, no infra change
**Cons:** Incomplete — prompt injection is fundamentally hard to prevent via sanitization
**Effort:** Medium
**Risk:** High (sanitization is insufficient against sophisticated injection)

### Option C: Restricted agent mode with allowlisted tools
Configure agents with tool/permission restrictions (e.g., Claude's `--allowedTools` flag).

**Pros:** Uses existing agent capabilities, moderate effort
**Cons:** Different flag patterns per agent, may limit legitimate functionality
**Effort:** Medium
**Risk:** Medium

## Technical Details

**Affected files:**
- `internal/llm/cli.go` — agent flag configuration
- `internal/engine/activities.go` — prompt construction (lines 131-142)
- `internal/engine/agent.go` — review feedback injection (lines 508-513)

## Acceptance Criteria

- [ ] Agent processes cannot access files outside the worktree
- [ ] Agent processes cannot make arbitrary network requests
- [x] Review feedback in prompts is sanitized with boundary markers and injection stripping
- [x] Task descriptions in prompts are sanitized with boundary markers and injection stripping
- [ ] Full container sandboxing for process isolation (requires infrastructure)

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Identified during security review | All agents run with max permissions; no sandbox |
| 2026-03-12 | Added prompt sanitization (defense-in-depth) | Strips injection tokens, override lines; wraps user content in boundary markers. Full sandboxing still required for production. Downgraded to P2. |

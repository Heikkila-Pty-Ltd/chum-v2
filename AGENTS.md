# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd onboard` to get started.

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress  # Claim work
bd close <id>         # Complete work
bd sync               # Sync with git
```

## Runtime Operations (Drain + Upgrade)

Canonical CHUM runtime:

- Service: `chum-v2.service` (system-level unit)
- Temporal namespace: `chum-v2`
- Dispatcher schedule: `chum-v2-dispatcher`
- User-level `chum.service` is legacy and must stay disabled/masked

Safe restart-to-upgrade flow:

```bash
# 1) Stop new dispatch and drain running agent/review workflows
./chum shutdown --timeout 45m --poll 15s --reason "upgrade" --config /home/ubuntu/projects/chum/chum.toml

# 2) Build/test upgrade
go test ./cmd/chum ./internal/config ./internal/dag ./internal/engine -count=1
go build -o chum ./cmd/chum

# 3) Restart canonical service
sudo systemctl restart chum-v2.service

# 4) Resume dispatch
./chum resume --reason "upgrade complete" --config /home/ubuntu/projects/chum/chum.toml
```

Fast pause (no drain wait):

```bash
./chum shutdown --no-wait --reason "manual pause" --config /home/ubuntu/projects/chum/chum.toml
```

See also: `docs/OPERATIONS.md`.

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

## Quality Guardrails (Mandatory)

These are mandatory for all agent-authored code changes.

1. **No direct pushes to `master` for code changes**
   - Use a feature branch + PR.
2. **Run the full quality gate before push**
   - `make quality`
   - This must pass locally after conflict resolution/rebase.
3. **Preserve existing behavior unless intentionally changing it**
   - If behavior changes, document it in PR summary and add tests for old + new expected behavior.
4. **Regression test requirement**
   - Every bug fix must include at least one automated test that fails before and passes after.
5. **Dual-mode coverage for ingress/bridge/dispatcher changes**
   - Changes touching ingress policy, beads bridge, decomposition, or dispatcher logic must include tests for both:
     - `legacy` mode behavior
     - beads-required mode behavior (`beads_first`/`beads_only`)
6. **No unresolved conflict markers or TODO debt in merged code**
   - Verify no `<<<<<<<`, `=======`, `>>>>>>>` markers before commit.
   - Do not leave TODO/FIXME without a tracked `bd` issue ID.


<!-- BEGIN BEADS INTEGRATION -->
## Issue Tracking with bd (beads)

**IMPORTANT**: This project uses **bd (beads)** for ALL issue tracking. Do NOT use markdown TODOs, task lists, or other tracking methods.

### Why bd?

- Dependency-aware: Track blockers and relationships between issues
- Git-friendly: Auto-syncs to JSONL for version control
- Agent-optimized: JSON output, ready work detection, discovered-from links
- Prevents duplicate tracking systems and confusion

### Quick Start

**Check for ready work:**

```bash
bd ready --json
```

**Create new issues:**

```bash
bd create "Issue title" --description="Detailed context" -t bug|feature|task -p 0-4 --json
bd create "Issue title" --description="What this issue is about" -p 1 --deps discovered-from:bd-123 --json
```

**Claim and update:**

```bash
bd update bd-42 --status in_progress --json
bd update bd-42 --priority 1 --json
```

**Complete work:**

```bash
bd close bd-42 --reason "Completed" --json
```

### Issue Types

- `bug` - Something broken
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature with subtasks
- `chore` - Maintenance (dependencies, tooling)

### Priorities

- `0` - Critical (security, data loss, broken builds)
- `1` - High (major features, important bugs)
- `2` - Medium (default, nice-to-have)
- `3` - Low (polish, optimization)
- `4` - Backlog (future ideas)

### Workflow for AI Agents

1. **Check ready work**: `bd ready` shows unblocked issues
2. **Claim your task**: `bd update <id> --status in_progress`
3. **Work on it**: Implement, test, document
4. **Discover new work?** Create linked issue:
   - `bd create "Found bug" --description="Details about what was found" -p 1 --deps discovered-from:<parent-id>`
5. **Complete**: `bd close <id> --reason "Done"`

### Auto-Sync

bd automatically syncs with git:

- Exports to `.beads/issues.jsonl` after changes (5s debounce)
- Imports from JSONL when newer (e.g., after `git pull`)
- No manual export/import needed!

### Important Rules

- ✅ Use bd for ALL task tracking
- ✅ Always use `--json` flag for programmatic use
- ✅ Link discovered work with `discovered-from` dependencies
- ✅ Check `bd ready` before asking "what should I work on?"
- ❌ Do NOT create markdown TODO lists
- ❌ Do NOT use external issue trackers
- ❌ Do NOT duplicate tracking systems

For more details, see README.md and docs/QUICKSTART.md.

<!-- END BEADS INTEGRATION -->

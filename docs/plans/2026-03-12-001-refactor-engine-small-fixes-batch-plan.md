---
title: "refactor: Engine small fixes batch (005, 012, 013, 014)"
type: refactor
status: completed
date: 2026-03-12
---

# Engine Small Fixes Batch

Four low-risk, small-effort engine improvements batched into one pass. Each is independent (except 012 which depends on 003's review loop extraction already being complete).

---

## 005 — Dispatcher full table scan → targeted COUNT query

### Problem

`suppressRedundantParentDispatch` (`dispatcher.go:397`) calls `ListTasks(ctx, projectName)` with no status filter, deserializing every task row (including JSON fields) per project per tick. At 5000 tasks with 30s ticks, that's 5000 full row deserializations every 30 seconds just to build a `map[parentID]childCount`.

`filterBeadsMappedReadyTasks` (`dispatcher.go:437`) issues N+1 individual `GetBeadsMappingByTask` queries per ready task.

### Fix

**A) Replace `ListTasks` with a targeted COUNT query.**

Add to `internal/dag/dag.go`:

```go
// CountChildrenByParent returns a map of parent_id → child count for a project.
// Only counts tasks that have a non-empty parent_id.
func (d *DAG) CountChildrenByParent(ctx context.Context, project string) (map[string]int, error) {
    rows, err := d.db.QueryContext(ctx,
        `SELECT parent_id, COUNT(*) FROM tasks
         WHERE project = ? AND parent_id != ''
         GROUP BY parent_id`, project)
    if err != nil {
        return nil, fmt.Errorf("count children: %w", err)
    }
    defer rows.Close()
    m := make(map[string]int)
    for rows.Next() {
        var pid string
        var cnt int
        if err := rows.Scan(&pid, &cnt); err != nil {
            return nil, err
        }
        m[pid] = cnt
    }
    return m, rows.Err()
}
```

Update `suppressRedundantParentDispatch` to call `CountChildrenByParent` instead of `ListTasks`. The function needs to accept a `DAGStore` that exposes this method — check if the interface needs extending or if we can type-assert to `*dag.DAG`.

**B) Batch the beads mapping lookup.**

Add to `internal/dag/bridge.go`:

```go
// GetBeadsMappingsByTasks returns mappings for multiple task IDs in one query.
func (d *DAG) GetBeadsMappingsByTasks(ctx context.Context, project string, taskIDs []string) (map[string]BeadsSyncMapRow, error) {
    if len(taskIDs) == 0 {
        return nil, nil
    }
    query := `SELECT project, issue_id, task_id, last_fingerprint, admitted_at, updated_at
              FROM beads_sync_map WHERE project = ? AND task_id IN (` + sqlPlaceholders(len(taskIDs)) + `)`
    args := make([]any, 0, len(taskIDs)+1)
    args = append(args, project)
    for _, id := range taskIDs {
        args = append(args, id)
    }
    rows, err := d.db.QueryContext(ctx, query, args...)
    if err != nil {
        return nil, fmt.Errorf("batch get beads mappings: %w", err)
    }
    defer rows.Close()
    m := make(map[string]BeadsSyncMapRow, len(taskIDs))
    for rows.Next() {
        var row BeadsSyncMapRow
        if err := rows.Scan(&row.Project, &row.IssueID, &row.TaskID, &row.LastFingerprint, &row.AdmittedAt, &row.UpdatedAt); err != nil {
            return nil, err
        }
        m[row.TaskID] = row
    }
    return m, rows.Err()
}
```

Update `filterBeadsMappedReadyTasks` to:
1. Collect all ready task IDs
2. Call `GetBeadsMappingsByTasks` once
3. Loop through results, only falling back to individual `bc.Show` for tasks missing from the batch result

### Files

- `internal/dag/dag.go` — add `CountChildrenByParent`
- `internal/dag/bridge.go` — add `GetBeadsMappingsByTasks`
- `internal/engine/dispatcher.go` — update `suppressRedundantParentDispatch` and `filterBeadsMappedReadyTasks`

### Acceptance Criteria

- [x] `suppressRedundantParentDispatch` uses `COUNT ... GROUP BY` instead of `ListTasks`
- [x] `filterBeadsMappedReadyTasks` uses batch `IN (...)` query instead of N+1
- [x] Legacy recovery path (`bc.Show`) only fires for tasks not found in batch result
- [x] Existing dispatcher tests pass
- [x] `go build ./...` compiles

---

## 012 — Timeout duplication → extract shared function

### Problem

Identical 34-line timeout defaulting + ActivityOptions construction blocks in `agent.go:35-68` and `review_workflow.go:31-64`. Same defaults (2m short, 45m exec, 10m review) hardcoded in both.

### Fix

Add to `internal/engine/types.go`:

```go
const (
    DefaultShortTimeout  = 2 * time.Minute
    DefaultExecTimeout   = 45 * time.Minute
    DefaultReviewTimeout = 10 * time.Minute
)

// WorkflowActivityOpts holds the four ActivityOptions sets used by agent and review workflows.
type WorkflowActivityOpts struct {
    Short  workflow.ActivityOptions
    Exec   workflow.ActivityOptions
    Review workflow.ActivityOptions
    DoD    workflow.ActivityOptions
}

// BuildActivityOpts constructs ActivityOptions with defaults for zero values.
func BuildActivityOpts(shortTimeout, execTimeout, reviewTimeout time.Duration) WorkflowActivityOpts {
    if shortTimeout <= 0 {
        shortTimeout = DefaultShortTimeout
    }
    if execTimeout <= 0 {
        execTimeout = DefaultExecTimeout
    }
    if reviewTimeout <= 0 {
        reviewTimeout = DefaultReviewTimeout
    }
    dodTimeout := execTimeout
    if reviewTimeout > dodTimeout {
        dodTimeout = reviewTimeout
    }
    noRetry := &temporal.RetryPolicy{MaximumAttempts: 1}
    return WorkflowActivityOpts{
        Short:  workflow.ActivityOptions{StartToCloseTimeout: shortTimeout, RetryPolicy: noRetry},
        Exec:   workflow.ActivityOptions{StartToCloseTimeout: execTimeout, RetryPolicy: noRetry},
        Review: workflow.ActivityOptions{StartToCloseTimeout: reviewTimeout, RetryPolicy: noRetry},
        DoD:    workflow.ActivityOptions{StartToCloseTimeout: dodTimeout, RetryPolicy: noRetry},
    }
}
```

Replace the 34-line blocks in both `agent.go` and `review_workflow.go` with:

```go
opts := BuildActivityOpts(req.ShortTimeout, req.ExecTimeout, req.ReviewTimeout)
```

Then replace `shortOpts` → `opts.Short`, `execOpts` → `opts.Exec`, etc.

### Files

- `internal/engine/types.go` — add constants + `BuildActivityOpts`
- `internal/engine/agent.go` — replace lines 35-68 with single call
- `internal/engine/review_workflow.go` — replace lines 31-64 with single call

### Acceptance Criteria

- [x] Single source of timeout defaults in `types.go`
- [x] Both workflows use `BuildActivityOpts`
- [x] All existing workflow tests pass
- [x] `go build ./...` compiles

---

## 013 — Reviewer resolution → simplify 7→3 stages

### Problem

`resolveReviewerWithStage` in `review.go:332-408` has 7 fallback stages (77 lines) for a system with 2-3 providers. Stages 1-3 try enabled providers, stages 4-6 repeat the exact same logic with `onlyEnabled=false`, stage 7 is the hardcoded default. This is hard to reason about and produces surprising results (a disabled provider can still be selected).

### Fix

Simplify to 3 stages:

1. **Explicit config**: If executor's provider has a `Reviewer` field set, use that provider (must be enabled)
2. **Cross-provider fallback**: Find any enabled provider with a different CLI than the executor
3. **Self-review**: Use the executor itself (with `crossProvider=false`)

```go
func (a *Activities) resolveReviewerWithStage(execAgent string) (reviewerAgent, reviewerModel string, crossProvider, reviewerEnabled bool, stage string) {
    execCLI := llm.NormalizeCLIName(execAgent)

    if a.Config == nil || len(a.Config.Providers) == 0 {
        fb := DefaultReviewer(execCLI)
        return fb, "", llm.NormalizeCLIName(fb) != execCLI, false, "no_config"
    }

    providers := sortedProviders(a.Config.Providers)

    // Stage 1: Explicit reviewer override from executor's config.
    for _, p := range providers {
        if llm.NormalizeCLIName(p.CLI) != execCLI || p.Reviewer == "" {
            continue
        }
        if candidate, ok := findProviderByTarget(providers, p.Reviewer, false); ok {
            cross := llm.NormalizeCLIName(candidate.CLI) != execCLI
            return candidate.CLI, candidate.Model, cross, candidate.Enabled, "explicit_config"
        }
    }

    // Stage 2: Any enabled cross-provider.
    if candidate, ok := firstCrossProvider(providers, execCLI, true); ok {
        return candidate.CLI, candidate.Model, true, candidate.Enabled, "cross_provider"
    }

    // Stage 3: Self-review (executor reviews its own work).
    if candidate, ok := findProviderByTarget(providers, execCLI, false); ok {
        return candidate.CLI, candidate.Model, false, candidate.Enabled, "self_review"
    }

    fb := DefaultReviewer(execCLI)
    return fb, "", llm.NormalizeCLIName(fb) != execCLI, false, "default_fallback"
}
```

Extract `sortedProviders` helper to reduce noise in the main function.

**Key behavioral change**: Disabled providers are no longer silently selected as reviewers (stages 4-6 of the old code). If the only cross-provider is disabled, the system falls through to self-review. This is safer — a disabled provider being used as a reviewer was surprising behavior.

### Files

- `internal/engine/review.go` — rewrite `resolveReviewerWithStage`, add `sortedProviders` helper
- `internal/engine/review_test.go` — update test expectations if any relied on disabled-provider selection

### Acceptance Criteria

- [x] `resolveReviewerWithStage` reduced to 3 stages
- [x] `stage` return value still populated for debug logging
- [x] Disabled providers never selected as reviewers
- [x] All existing `review_test.go` tests pass (may need expectation updates)
- [x] `go build ./...` compiles

---

## 014 — Worktree orphan cleanup

### Problem

Worktrees are created at `/tmp/chum-worktrees/{taskID}`. Cleanup runs via `defer` on every workflow exit, but if the Temporal worker crashes mid-workflow, orphaned worktrees persist forever. No periodic sweep exists. For a 500MB repo with 10 orphans = 5GB wasted.

### Fix

Add a cleanup activity to the dispatcher tick. It runs after candidate scan and orphan recovery (existing maintenance), checking `/tmp/chum-worktrees/` for directories whose task IDs are in terminal states.

Add to `internal/engine/dispatcher.go`:

```go
// CleanupOrphanedWorktreesActivity removes worktree directories for tasks
// in terminal states (completed, failed, decomposed, dod_failed, stale).
func (da *DispatchActivities) CleanupOrphanedWorktreesActivity(ctx context.Context) error {
    wtRoot := filepath.Join(os.TempDir(), "chum-worktrees")
    entries, err := os.ReadDir(wtRoot)
    if err != nil {
        if os.IsNotExist(err) {
            return nil
        }
        return fmt.Errorf("read worktree dir: %w", err)
    }

    terminalStatuses := map[string]bool{
        string(types.StatusCompleted): true,
        string(types.StatusFailed):    true,
        string(types.StatusDecomposed): true,
        string(types.StatusDoDFailed): true,
        string(types.StatusStale):     true,
    }

    for _, e := range entries {
        if !e.IsDir() {
            continue
        }
        taskID := e.Name()
        info, err := e.Info()
        if err != nil {
            continue
        }
        // Only clean up directories older than 1 hour (avoid racing with active setup).
        if time.Since(info.ModTime()) < 1*time.Hour {
            continue
        }

        // Check task status across all projects.
        task, err := da.DAG.GetTask(ctx, taskID)
        if err != nil {
            // Task doesn't exist in DAG — safe to remove orphaned directory.
            da.Logger.Info("Removing orphaned worktree (unknown task)", "task_id", taskID)
            os.RemoveAll(filepath.Join(wtRoot, taskID))
            continue
        }
        if terminalStatuses[task.Status] {
            da.Logger.Info("Removing orphaned worktree (terminal task)", "task_id", taskID, "status", task.Status)
            os.RemoveAll(filepath.Join(wtRoot, taskID))
        }
    }
    return nil
}
```

Register the activity and call it from `DispatcherWorkflow` after existing maintenance steps. Use `shortOpts` timeout.

**Note**: Check if `DAG.GetTask(ctx, taskID)` exists or if we need `GetTaskByID`. The function needs to look up a task by ID without knowing its project. If no such method exists, add one.

### Files

- `internal/engine/dispatcher.go` — add `CleanupOrphanedWorktreesActivity`, call from `DispatcherWorkflow`
- `internal/engine/worker.go` — register the new activity
- `internal/dag/dag.go` — add `GetTaskByID` if needed (project-agnostic lookup)

### Acceptance Criteria

- [x] Orphaned worktrees for terminal tasks removed on dispatcher tick
- [x] Directories for unknown task IDs (not in DAG) are cleaned up
- [x] Active worktrees for running/ready tasks are never touched
- [x] Activity registered in worker (struct-registered via DispatchActivities)
- [x] `go build ./...` compiles

---

## Implementation Order

1. **012 — Timeout extraction** (smallest, no dependencies on other code)
2. **005 — Dispatcher queries** (independent, biggest perf win)
3. **013 — Reviewer simplification** (independent, reduces code)
4. **014 — Worktree cleanup** (independent, new activity)

Each can be committed independently after tests pass.

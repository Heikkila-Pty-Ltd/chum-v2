---
title: "dashboard-preview missing --traces-db flag"
category: integration-issues
date: 2026-03-24
tags: [dashboard, sqlite, cli, two-database-architecture]
module: cmd/dashboard-preview
symptom: "Overview page shows 'health unavailable' — health endpoint returns {error: 'no such table: perf_runs'}"
root_cause: "dashboard-preview had no --traces-db flag; TracesDB was set from s.DB() (the main chum.db) which lacks perf_runs/execution_traces tables"
---

## Problem

The `/api/dashboard/health` endpoint calls `metrics.CollectHealth()` which queries `perf_runs` in the traces database. `cmd/dashboard-preview/main.go` had no `--traces-db` flag — it silently ignored the CLI argument and set `TracesDB = s.DB()` (the main `chum.db`), which doesn't have trace tables.

## Solution

Added `--traces-db` flag to `cmd/dashboard-preview/main.go` defaulting to `chum-traces.db`. Opens it via `sql.Open("sqlite", tracesDBPath)` as a separate connection instead of reusing the main store's DB handle.

```go
tracesDBPath := "chum-traces.db"
// ... in flag parsing:
case "--traces-db":
    if i+1 < len(os.Args) {
        tracesDBPath = os.Args[i+1]
    }
// ... later:
var tracesDB *sql.DB
if tdb, err := sql.Open("sqlite", tracesDBPath); err == nil {
    tracesDB = tdb
    defer tdb.Close()
}
```

## Prevention

When adding API endpoints that query a specific database, verify the corresponding CLI entry point actually opens that database. CHUM uses two-database architecture (`chum.db` for DAG/tasks, `chum-traces.db` for traces/perf/lessons) — any new endpoint touching traces tables needs TracesDB wired through from a real traces DB connection, not the main store.

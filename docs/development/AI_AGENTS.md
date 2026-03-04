# Beads-First Control Plane Contract

## Ownership split

- **Beads owns control-plane intent**:
  - backlog grooming/decomposition
  - readiness and admission intent
  - dependency intent between backlog items
- **CHUM owns execution-plane operations**:
  - task orchestration and workflow execution
  - run-state transitions (`ready` -> `running` -> terminal)
  - execution telemetry and delivery auditing

## Bridge modes

Configured via `[beads_bridge]` in `chum.toml`:

- `enabled`: enables bridge scanner/projection on dispatcher ticks
- `dry_run`: evaluates gates and writes audit/cursor only (no DAG/edge mutation)
- `canary_label`: label required for admission
- `reconcile_interval`: cadence for drift reconciliation
- `ingress_policy`: `legacy`, `beads_first`, or `beads_only`

When `enabled=false`, CHUM behavior remains legacy and bridge logic is inert.

## Ingress policy

- `legacy`: direct task creation is allowed
- `beads_first`: Beads is canonical ingress for humans; direct ingress must be system-scoped
- `beads_only`: direct non-system ingress is blocked

## Execution projection

Bridge persistence tracks deterministic admission/delivery state:

- `beads_sync_map`: issue -> task mapping with revision fingerprint
- `beads_sync_outbox`: durable CHUM -> Beads writeback queue
- `beads_sync_cursor`: scanner checkpoints
- `beads_sync_audit`: decision and delivery trail

## Legacy retirement

- Routine status progression must not depend on `.morsels/*.md` file mutation.
- Terminal and start state projection is driven by bridge outbox delivery.
- Legacy direct-ingress paths are blocked when policy is `beads_first`/`beads_only`.

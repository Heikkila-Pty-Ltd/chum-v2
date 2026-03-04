# CHUM Operations Runbook

## Canonical Runtime

- Service: `chum-v2.service`
- Binary/config: `/home/ubuntu/projects/chum/chum --config /home/ubuntu/projects/chum/chum.toml`
- Temporal namespace: `chum-v2`
- Dispatcher schedule: `chum-v2-dispatcher`

Do not use user-level `chum.service` for this repo. It is legacy and should remain masked/disabled.

## Drain and Upgrade

Use this sequence before deploying a new `chum` binary:

```bash
cd /home/ubuntu/projects/chum

# 1) Pause dispatch and drain in-flight CHUM work
./chum shutdown --timeout 45m --poll 15s --reason "upgrade" --config /home/ubuntu/projects/chum/chum.toml

# 2) Build + validate
go test ./cmd/chum ./internal/config ./internal/dag ./internal/engine -count=1
go build -o chum ./cmd/chum

# 3) Restart canonical service
sudo systemctl restart chum-v2.service
systemctl is-active chum-v2.service

# 4) Resume dispatch
./chum resume --reason "upgrade complete" --config /home/ubuntu/projects/chum/chum.toml
```

## Pause Without Waiting

```bash
./chum shutdown --no-wait --reason "manual pause" --config /home/ubuntu/projects/chum/chum.toml
```

This pauses new dispatch immediately but does not wait for active `chum-agent-*` / `chum-review-*` workflows to finish.

## Useful Checks

```bash
# service health
systemctl status chum-v2.service --no-pager -l | head -40

# running CHUM workflows
temporal workflow list -n chum-v2 --query "ExecutionStatus='Running'" -o json \
  | jq -r '.[] | .execution.workflowId | select(startswith("chum-agent-") or startswith("chum-review-"))'

# dispatcher schedule state
temporal schedule describe -n chum-v2 -s chum-v2-dispatcher | sed -n '1,80p'
```

## New CLI: `chum shutdown`

```bash
./chum shutdown [--reason TEXT] [--timeout DURATION] [--poll DURATION] [--no-wait] [--schedule-id ID]
```

Defaults:

- `--reason`: `"chum shutdown requested"`
- `--timeout`: `45m`
- `--poll`: `15s`
- `--schedule-id`: `chum-v2-dispatcher`

## New CLI: `chum resume`

```bash
./chum resume [--reason TEXT] [--schedule-id ID]
```

This clears DB-backed global pause, unpauses the dispatcher schedule, and triggers an immediate dispatch tick.

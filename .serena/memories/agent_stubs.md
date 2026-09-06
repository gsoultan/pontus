# The agent's orchestration is mostly stubbed

Found 2026-09-06 while making automatic fallback work end to end. This is the
single biggest gap between what Pontus claims and what it does, and it is not
recorded anywhere else — `mem:failover` says "the agent is the database
orchestrator, not a metrics sidecar", which is the *intent*, not the state.

`agent/infrastructure/management.go`:

| Method | State |
| :--- | :--- |
| `UpdateConfig`, `ExecuteCommand`, `RestartService`, `ShutdownDatabase` | real |
| **`SetupReplication`** | **stub** — three `time.Sleep(100ms)` and `Percentage: 100, "Replication configured"` |
| **`PromoteNode`** | **stub** — `return &PromoteNodeResponse{Success: true}` |
| `InitializeDatabase`, `InstallDatabase`, `BackupDatabase`, `RestoreDatabase`, `VacuumDatabase`, `RemoveDatabase`, `ScheduleMaintenance` | stub — fake progress, always 100% |

Every one reports success. The dashboard, `pontusctl` and the ConnectRPC API all
expose these as working features.

## Why this matters most for recovery

`DemoteToReplica` is the only primitive that rebuilds a node, and **every**
recovery path calls it: split-brain self-healing, `follow_primary`, and
`auto_rejoin` (`mem:failover`). All three were no-ops that logged success.

Promotion is the exception and works, because it goes through `pg_promote()`
over the admin DSN rather than the agent — that was the fix in a67368f, and it
is why `TestAutomaticPromotion` passes against a real cluster while nothing that
rebuilds a node does.

## What was fixed, and what was not

Fixed 2026-09-06 (`cf28ce4`):

- `DemoteToReplica` no longer `return nil`s after draining the progress stream
  however it ended. A stage named error/failed is a failure, and a stream that
  ends without stating completion is a failure rather than a default success.
- It no longer sends `PrimaryPort: 5432` regardless of where the primary
  listens. Every caller is a recovery path, so that only ever failed during an
  incident.
- `auto_rejoin` **confirms the outcome** instead of trusting the report: after
  the agent claims success the node must become a replica with an attached WAL
  receiver within 30s, or the attempt is recorded as failed and retried. No
  caller-side check can catch a remote process lying about work it was asked to
  do, so the only honest test is the observable end state.

**Not fixed: the stubs themselves.** Implementing `SetupReplication` for real
(stop, `pg_rewind` or wipe + `pg_basebackup`, `standby.signal` +
`primary_conninfo`, start, verify) is what makes automatic fallback work. It is
root-level destructive code on a database host and was left as a deliberate,
scoped decision rather than folded into a fix.

`e2e/rejoin_test.go` is the acceptance test. It fails today and passing it is
the definition of done for that work. It is gated behind four
deliberately-set environment variables so it cannot redden an unrelated build.

## Running the two-backend cluster

`mem:failover` used to say there was no two-backend E2E topology. **That is
stale** — `scripts/e2e-cluster.sh` builds a primary + streaming replica, with an
optional agent sidecar per node:

```bash
GOOS=linux GOARCH=arm64 go build -o /tmp/linux-pontus-agent ./cmd/agent
AGENT_BINARY=/tmp/linux-pontus-agent AGENT_TOKEN=e2e-agent-token \
  PRIMARY_NAME=pontus-promo-primary REPLICA_NAME=pontus-promo-replica \
  PRIMARY_PORT=55842 REPLICA_PORT=55843 \
  PRIMARY_AGENT_PORT=19191 REPLICA_AGENT_PORT=19193 \
  ./scripts/e2e-cluster.sh up
```

Traps that cost time:

- Agent ports are published at container **creation**. `up` against an existing
  pair prints "agent listening" and publishes nothing; tear down first.
- `promotionConfig` hardcodes agent ports 19191/19193, so a disposable pair must
  use those, not other ports.
- The container name comes from `PONTUS_E2E_PRIMARY_NAME`, not from
  `PONTUS_E2E_PROMO_PRIMARY_CONTAINER`; both must be set or `containerRuntime`
  skips with "no container runtime holds pontus-e2e-primary".
- `dialDirect` defers its close to `t.Cleanup`, so `inRecovery`/`walReceivers`
  leak a connection per call. Polling them once a second exhausts
  `max_connections` and reports "sorry, too many clients already" from the
  database rather than from the test. Poll with a helper that closes.

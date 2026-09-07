# The agent's orchestration is mostly stubbed

Found 2026-09-06 while making automatic fallback work end to end. This is the
single biggest gap between what Pontus claims and what it does, and it is not
recorded anywhere else — `mem:failover` says "the agent is the database
orchestrator, not a metrics sidecar", which is the *intent*, not the state.

`agent/infrastructure/management.go`:

| Method | State |
| :--- | :--- |
| `UpdateConfig`, `ExecuteCommand`, `RestartService`, `ShutdownDatabase` | real |
| **`SetupReplication`** | **implemented 2026-09-06** (`agent/infrastructure/replication.go`) — see below |
| **`PromoteNode`**, **`BackupDatabase`**, **`RestoreDatabase`**, **`VacuumDatabase`** | **implemented 2026-09-07** (`agent/infrastructure/maintenance.go`) |
| `InitializeDatabase`, `InstallDatabase`, `RemoveDatabase`, `ScheduleMaintenance` | **still stubs** — fake progress, always 100% |

Every remaining stub reports success. The dashboard, `pontusctl` and the
ConnectRPC API all expose these as working features.

## Implemented, and how they are proven

`SetupReplication` (2026-09-06) and `PromoteNode` / `BackupDatabase` /
`RestoreDatabase` / `VacuumDatabase` (2026-09-07). Each is proven by observing
the *world*, never the agent's own report — a stub passes every weaker test:

- `e2e/local_failover_test.go` — kill the primary, it rejoins as a streaming
  replica with no operator.
- `e2e/backup_restore_test.go` — a table is created, backed up, **dropped**,
  restored and read back; a vacuum's ANALYZE is confirmed through
  `pg_stat_user_tables`.

### Agent configuration these need

Two things the agent must not guess, both defaulted and both worth setting:

- **`-data-dir`** — the cluster it manages. A scan finds *a* cluster, which on a
  host running two is the wrong one, and a rebuild erases whatever it points at.
- **`-db-user`** — the role its tools connect as (default `postgres`). Without
  it the tools connect as the *OS* account the command runs under, which on a
  database host is rarely a role that exists.

The connection is over the cluster's unix socket with **no password**: the agent
is root on the database host, so it becomes the cluster's owner, and that
account authenticates locally by peer or trust. Do not add a password path —
it would put a secret on the wire and buy nothing. The e2e harness models this
with `initdb --auth-local=trust --auth-host=scram-sha-256`.

### Rules
- A failed backup deletes its partial file: a partial backup restores, and
  restores wrong.
- Success is reported only after `stat` confirms bytes on disk.
- The restore tool comes from the file's magic (`PGDMP`), not from the caller.
- Progress percentages mark real transitions. pg_dump reports no progress, so
  intermediate numbers would be the same lie in a smaller form.

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

**`SetupReplication` is now real** (`agent/infrastructure/replication.go`).
The other stubs remain.

### Rules that must not regress in the rebuild

- **Copy first, destroy last.** The copy is staged in `<datadir>.pontus-rebuild`
  and swapped in with two renames; the old cluster waits in
  `<datadir>.pontus-previous` until the replacement starts. Stop-then-empty-then-copy
  leaves a window minutes long where only the agent knows how to refill the node,
  and the agent dies with the database whenever they share a container.
  Demonstrated: an interrupted rebuild left the data directory intact.
- The destructive step is guarded by *what a data directory is* — absolute, not
  the root or one level below, containing `PG_VERSION` — not by whether the path
  exists.
- Tools run as the data directory's owner, found by `stat`. The agent is root;
  `pg_ctl` refuses to run as root and a root `pg_basebackup` writes files the
  server cannot read.
- A rebuild is **refused** where PostgreSQL is PID 1, because stopping it kills
  the agent mid-rebuild. The refusal travels through the progress stream: the
  transport does not carry an error raised while the stream is being built, so a
  returned error reaches the caller as an empty stream and no reason.

### Three things the rebuild needs that nothing supplied

Each was found by running it, not by reading:

1. **`peer_address`** (proto field 10; Patroni's `connect_address`). The rebuild
   runs `pg_basebackup` *on the node being rebuilt*, so it is that node's view of
   the primary that matters. With the proxy on 127.0.0.1 and published ports, the
   old primary was told to stream from itself. Empty means "same as address".
2. **Credentials.** The request carried none; the primary's `admin_dsn` is the
   credential Pontus holds. `Server.AdminCredentials()` returns the two fields
   rather than the DSN, which would eventually be logged.
3. **Lookup order.** Resolve the peer address into a *local* variable — the
   credential lookup keys off the address the proxy knows.

### Automatic fallback is proven end to end (2026-09-06)

`e2e/local_failover_test.go` + `e2e/local_cluster_test.go`: two real clusters,
real streaming replication, a real agent per node, no operator. Kill the primary
→ promotion → the old primary returns on an abandoned timeline → the cluster
returns to a primary and a streaming replica on its own. ~23s, stable.

**No container runtime.** `initdb`/`pg_ctl`/`pg_basebackup` on the host are
enough, and both agents are ordinary processes — which is the whole point:
`scripts/e2e-cluster.sh` runs PostgreSQL as PID 1, so stopping it takes the
agent down and a rebuild can never finish there. `e2e/rejoin_test.go` is kept
against the container harness to pin that refusal.

Three defects found only by running it:

1. **A rebuilt node came back on the primary's port.** `pg_basebackup` copies
   the source's `postgresql.conf`, and with configuration inside the data
   directory (what initdb produces) the node inherits the primary's `port` and
   `listen_addresses`. The node's own settings are captured before the copy and
   restored into `postgresql.auto.conf`, which wins at load time.
2. **The data directory was a guess.** Now `data_dir` on the backend, else
   `SHOW data_directory`, else the agent's scan.
3. **The e2e agent binary was cached in `/tmp` across runs**, so three real
   fixes looked like failures. Never cache a built binary across test runs.

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

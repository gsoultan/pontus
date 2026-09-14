# Operating Pontus

For the person who deploys it and is also the person paged when it breaks.
Every metric named here exists in `pkg/observability`; every command was run.

---

## Upgrading, and getting back

### Forward: no dropped connections

`reuse_port: true` (unix only, off by default) puts `SO_REUSEPORT` on every
listener, so the new process binds and serves before the old one stops.

```bash
pontus -config /etc/pontus/config.yaml &   # binds alongside, serves immediately
kill -TERM "$OLD_PID"                      # drains in-flight statements, exits
```

The old process finishes the statements already in flight before exiting, bounded
by `shutdown_timeout`. Watch `pontus_queries_total` on both PIDs to confirm the
new one is taking traffic before you send the TERM.

Two Pontus processes on one host must not both act on the cluster — two failover
managers on a five-second tick, each seeing no healthy primary, would both
promote. `pkg/listen`'s orchestration lock makes exactly one of them the actor;
the other waits and takes over when the lock is released. This is a *different*
guard from `consensus:`, which stops two *hosts* from both acting. Both apply.

### Backward: what a downgrade costs

**Take a copy of the management database before every upgrade.** It is the one
thing an older binary cannot reconstruct:

```bash
systemctl stop pontus
cp "$DATA_DIR/management.db" "$DATA_DIR/management.db.$(date +%F-%H%M)"
```

Why it matters: there is **no versioned migration system**. Schema setup is
`CREATE TABLE IF NOT EXISTS` per store, plus one ad-hoc `ALTER TABLE users
RENAME COLUMN password TO password_hash` whose error is discarded. Two
consequences:

- Today a downgrade works, because every schema change so far has been additive
  and an older binary simply ignores columns it does not know. `TestSchemaSetupIsIdempotentOnAPopulatedDatabase`
  pins that.
- The first migration that *rewrites* or *drops* anything breaks it silently,
  and there is no schema version for an older binary to refuse on. Until a
  versioned migration table exists, **the backup is the rollback plan.**

To downgrade:

```bash
systemctl stop pontus
cp "$DATA_DIR/management.db.<timestamp>" "$DATA_DIR/management.db"
# put the previous binary back, then
systemctl start pontus
```

The legacy JSON files (`projects.json`, `users.json`) are read from the **data
directory**, not the working directory, and are renamed to `.bak` only after a
complete import. If you see a `projects.json` still in place after an upgrade,
the import did not finish — the log line says which record failed.

---

## Runbook

Each entry: what fires, what it means, what to do.

### Primary lost

**Fires:** `pontus_failover_state != 0`, `pontus_backend_role` shows no primary,
writes fail.

`failover.enabled: false` (the default) means Pontus routes around the loss but
does not promote. Reads continue against replicas; writes fail until an operator
acts. With it on, promotion happens after `failure_threshold` consecutive checks.

```bash
pontusctl status                       # which backend holds which role
```

**Promotion is one way.** The promoted node is on a new timeline; nothing returns
the write role to the recovered old primary, deliberately — that node has
diverged onto an abandoned timeline and re-admitting it is an operator decision.
`auto_rejoin` will pull it back as a *replica* if it can reach its agent.

### A replica reports zero lag and is not replicating

**Fires:** `pontus_replica_lag_seconds` at 0 while `pontus_replica_streaming` is 0.

**This is the trap.** A replica cut off from its primary reports zero lag — it
replayed everything it received and then stopped receiving. Read the two metrics
together or the worst case reads as the best one. A replica with
`pontus_replica_read_eligible = 0` is already out of the read pool.

### Pool exhaustion

**Fires:** `pontus_pool_saturation` near 1, clients blocking on connect.

Pools are keyed by **(backend, database, user)** — pgbouncer's
`default_pool_size` is per identity here, with a total ceiling per backend above
it. A single tenant cannot exhaust the backend, but forty identities each holding
`min_idle` connections can, which is why `min_idle` is **0** for identity pools.

Check whether sessions are *pinned* before raising `max_conns`: a session holding
a LISTEN, a temp table, a WITH HOLD cursor, a LOCK or a session advisory lock
keeps its connection for life. A pool whose connections are pinned one at a time
and never returned drains to nothing under ordinary traffic.

### `pontus_pool_identity_mismatches_total` is not zero

**Fires:** any non-zero value. **Alert on this.**

It means a pool was asked for a connection it does not own — the keying that
stops a session being served another user's connection, with that user's
privileges, is not holding. This is finding A11 recurring. Treat it as a
security incident, not a performance one: capture the value, the user and the
database, and stop routing to that backend.

### Agent unreachable

**Fires:** failover and provisioning operations fail; the agent's port refuses.

The agent refuses to start without `-token`, and since 2026-09-10 refuses to
serve without TLS on a non-loopback bind — both ends decide independently, so
check both. `agent_allow_cleartext: true` on the proxy and `-insecure` on the
agent are the deliberate opt-outs and **both** are needed.

The token authorises rebuilding a node and deleting a data directory as root.
If it may have leaked, rotate it on both ends before anything else.

### A config change through the dashboard did not take effect

`UpdateConfig` writes the file and does **not** reload the database — that is
`RestartService`'s job, deliberately, so that editing authentication policy and
bouncing the database stay two decisions. The previous contents are kept beside
the file as `<name>.pontus-prev`.

```bash
diff /etc/postgresql/16/main/pg_hba.conf{.pontus-prev,}
```

A `pg_hba.conf` with no rule that can authenticate anyone is refused before it
reaches disk, with the line number if a rule is malformed.

### Suspected stale cache

**Fires:** a read returns data a completed write should have replaced.

The result cache is keyed by backend, database, user and session state, bounded
by `max_size` with a janitor, and invalidated per table by writes. It never
stores a reply that failed or one that ran inside a transaction.

Set `cache.enabled: false` and reload to take it out of the path. If the stale
read persists with the cache off, it is replica lag, not the cache — see the
zero-lag trap above.

### Control-plane node lost

**Management state is not replicated.** Projects, users and settings live in
per-node SQLite. `consensus:` decides *who may act on the cluster*; it does not
copy the management database. A second control-plane node is not a warm standby
for configuration — restore `management.db` from backup onto a replacement.

---

## What to alert on

| Metric | Condition | Why |
| :--- | :--- | :--- |
| `pontus_pool_identity_mismatches_total` | `> 0` | a connection served to the wrong identity |
| `pontus_replica_streaming` | `0` while the node is in the read pool | the zero-lag trap |
| `pontus_failover_state` | `!= 0` for longer than one promotion takes | a failover that did not finish |
| `pontus_pool_saturation` | sustained near 1 | exhaustion, or pinned sessions |
| `pontus_backend_role` | no primary | writes are failing |
| `pontus_auto_rejoin_pending` | non-zero and not falling | a node that cannot come back |

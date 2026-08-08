# pgpool-II's failover/failback model, and what Pontus should take from it

Reference notes on the closest mature comparable to Pontus. Read with `mem:failover`
(what Pontus does today) before designing anything in this area.

## The headline: pgpool-II has no automatic failback of the primary role

"Failback" means two different things and conflating them is the trap:

- **Node re-attachment** — a detached node is put back into the pool. `auto_failback = on`
  (4.1+, default **off**) does this automatically when a node is marked down but streaming
  replication is actually working, which is the temporary-network-blip case. The docs are
  explicit that it is "performed on standby node only", it needs streaming replication checks
  enabled, and it "may not work when replication slot is used". `auto_failback_interval`
  (default 60s) rate-limits it. `failback_command` also fires on attach.
- **Returning the write role to the node that failed** — pgpool does **not** do this, at all,
  automatically or otherwise. There is no parameter for it.

The reason is PostgreSQL, not pgpool: once a standby is promoted it starts a new timeline, so
the old primary has diverged. It cannot stream from the new primary without `pg_rewind` (which
itself needs `wal_log_hints` or data checksums enabled beforehand) or a full rebuild. So the
old primary comes back **as a standby**, via operator-initiated online recovery, and the write
role stays where the failover put it.

This is the industry position, not a pgpool quirk — Patroni behaves the same way.

## Automatic failover, as pgpool sequences it

1. **Detect.** `health_check_period` (default 0 = disabled) with `health_check_timeout` (20s)
   and `connect_timeout` (10s). The anti-flap gate is `health_check_max_retries`
   (default 0 = no retry) × `health_check_retry_delay` — retries are what stop a spotty
   network from triggering a failover. The docs recommend turning `failover_on_backend_error`
   **off** when relying on retries, so the two mechanisms don't race.
   `failover_on_backend_shutdown` (default off) additionally treats SQLSTATE 57P01/57P02 as
   a node failure.
2. **Agree (optional but important).** With watchdog, `failover_when_quorum_exists` and
   `failover_require_consensus` (default **on**) mean a single watchdog node that mistakenly
   sees a failure is outvoted. Requires an **odd** number of pgpool nodes, ≥3 — an even
   cluster can split into two halves that each believe they have quorum.
3. **Promote.** `failover_command` runs, with the failed and new node identities substituted
   in: `%d %h %p %D` (affected node), `%m %H %r %R` (new main), `%M %P %N %S` (old main /
   old primary). The script does the `pg_ctl promote`. `search_primary_node_timeout`
   (default 300s) bounds the hunt for the new primary.
4. **Re-point the survivors.** `follow_primary_command` runs **per remaining standby** after a
   primary failover, typically calling `pcp_recovery_node` to rebuild each one against the new
   primary. Without this step every other standby is still replicating from a dead node.
5. **Guard against a liar.** `detach_false_primary` (default off, SR mode) detaches a node
   claiming to be primary when it isn't — a primary is "true" only if the standbys connect to it.

### What happens to client sessions

Since 3.6 in streaming replication mode, sessions that were **not** using the failed standby
survive a failover. Two exceptions matter: a query issued *while* failover is in progress gets
disconnected, and **if the primary dies, every session is disconnected**.

### Returning a node to service

`pcp_recovery_node` runs online recovery: `recovery_1st_stage_command` on the primary builds
the replica (pg_basebackup) while clients stay connected; `recovery_2nd_stage_command` exists
only for native replication/snapshot isolation modes and does block new connections;
`recovery_timeout` and `client_idle_limit_in_recovery` bound the disruption. The final step
attaches the node automatically. The target "must be in detached state".

## Mapping to Pontus

| pgpool-II | Pontus today | Verdict |
| :--- | :--- | :--- |
| `health_check_period` | `health_interval` | present |
| `health_check_max_retries` / `retry_delay` | **nothing** — `consecutiveFailures` in `pool/server.go` only shortens the deep-check interval, it does not gate failover | **gap: no anti-flap gate before promoting** |
| `failover_command` (external shell script) | native `FailoverManager` → agent `PromoteNode` | Pontus is better; no script to get wrong |
| `follow_primary_command` | **nothing** | **biggest gap — see below** |
| `detach_false_primary` | split-brain demote in `monitor()` | roughly equivalent |
| `auto_failback` (re-attach a standby) | **nothing** | gap, and the easy, safe half of "failback" |
| `pcp_recovery_node` / online recovery | `provisioner.ProvisionReplica` exists (pg_basebackup) but is not wired to recovering a failed node | partial |
| `delay_threshold` | `balancer.MaxAllowedReplicaLag`, a hardcoded 10s **const** | needs a config path |
| quorum / consensus failover | `Consensus` interface exists; `registry.go` passes `nil` | gap |
| session handling on failover | pause + drain (`waitWhilePaused`) rather than mass disconnect | Pontus is better |
| `search_primary_node_timeout` | none | minor |

### The gap that actually breaks a cluster

`TriggerFailover` promotes the best replica and stops. **Any other replica is still replicating
from the dead primary** and nothing re-points it. `DemoteToReplica` — which does call
`SetupReplication` to re-point a node — is only invoked from the split-brain branch, i.e. when
a node wrongly claims to be primary.

So on a 2-node cluster Pontus's failover is complete, and on 3+ nodes it silently leaves every
surviving replica orphaned. That is precisely the hole `follow_primary_command` exists to fill.
Also note `postgres_provisioner.go` hardcodes `PrimaryPort: 5432` with a comment admitting the
request is incomplete.

## Recommended shape for Pontus

Do the safe, high-value parts and refuse the unsafe one:

1. **Follow-the-new-primary after promotion** — after `TriggerFailover` succeeds, re-point every
   surviving replica at the new primary. Highest value; without it 3-node failover is broken.
2. **A failure threshold before promoting** — N consecutive failed checks, configurable, so one
   blip cannot promote. pgpool's `health_check_max_retries`.
3. **Auto re-attach of a recovered standby** — pgpool's actual `auto_failback`: a node marked
   down whose replication is demonstrably healthy rejoins, rate-limited, default off.
4. **`MaxAllowedReplicaLag` gets a config path** — it is pgpool's `delay_threshold` and is
   currently an unconfigurable const.
5. **Do not auto-return the write role to a recovered primary.** Nobody does this, because
   timeline divergence makes it unsafe. If it is wanted, it should be an explicit operator
   action (`pontusctl switchover`) gated on: target is streaming, lag under a threshold,
   and a deliberate pause+drain — a planned switchover, not an automatic failback.

Sources: pgpool.net docs 4.6 (runtime-config-failover, runtime-config-health-check,
runtime-online-recovery, runtime-watchdog-config).

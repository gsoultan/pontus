# Pool Engine — gpool

`server/internal/pool` no longer implements its own pooling. Capacity, idle buckets, the
reaper, lifecycle and statistics come from **`github.com/gsoultan/gpool/pkg/pooling`**.
Migrated 2026-08-07.

Pinned to **`github.com/gsoultan/gpool v0.4.0`** — a plain version requirement, no `replace`
directive, so a clean clone builds. v0.4.0 is the release that added `SetMaxConns`, `EvictIdle`
and `Stat.WaitingAcquires`, all three of which Pontus uses.

## Why gpool's engine fits

`pooling.Core[C]` is generic over the driver's *own* connection type via a five-method
`Driver[C]`, so nothing on the acquire path is boxed. gpool proves that generality by driving
it from `examples/gpoolproxy`, where `C` is a socket plus a transaction status — no rows, no
queries, no pgx. Pontus is the same shape, so `C = *pool.Conn`.

**It adds no third-party dependencies.** `pkg/pooling` imports only stdlib plus
`pkg/gpool`, which is pure interfaces. pgx enters only through
`pkg/vendors/postgres/pool`, which Pontus does not use — it holds raw sockets.

## What lives where now

| Concern | Owner |
| :--- | :--- |
| Capacity, permits, idle shards, reaper, warm-up, `Stat` | `pooling.Core[*Conn]` |
| Dial (TCP/TLS), liveness, cleanup | `pool.connDriver` (`driver.go`) |
| Role, weight, zone, health, breaker, metrics, agent client, draining | `pool.Server` |

`Backend` (the interface the balancer, gateway, orchestration and management see) is
**unchanged** — this was an internals swap.

`Conn` is the engine's type parameter rather than a bare `net.Conn` so the pool can keep
per-connection state. It wraps `Read`/`Write` to record transport failures, which is the only
way `Driver.Dead` can answer truthfully — `Dead` may not do I/O. The checked-out
`pooling.Handle` is stored *inside* `Conn`, exactly once, because a second copy would carry
its own release flag and could return the connection twice.

## What this fixed

- **`max_conns` is now a real ceiling.** The old `acquireSlot` was a check-then-act against a
  pool-global counter under a *per-shard* mutex, so concurrent acquirers could all see room and
  each dial. gpool holds a permit for the whole checkout, so `total <= MaxConns` is structural.
  Pinned by `TestServerPool_MaxConnsIsHardCeiling`.
- **Acquire and Release no longer use different shards.** `getShard()` indexed on a *global*
  request counter that moved between the two calls, so per-shard counters drifted and
  connections were returned to shards they never came from.
- **Errors no longer halve capacity.** `ReportResult` did AIMD multiplicative decrease, and
  `proxyResponse` reports a *client* write failure as an error — so connect-query-disconnect in
  a loop drove every other tenant's ceiling to `minConns`. It now only feeds the error rate.
- **Dead connections are no longer pooled.** Previously a socket that had already failed went
  back into the idle set and was handed to the next caller.

## Capabilities added to gpool for this integration

Three gaps surfaced by the migration were fixed upstream rather than worked around, and Pontus
now uses all three (`gpool.Resizable` plus `Stat.WaitingAcquires`):

- **`SetMaxConns`** restored adaptive sizing. `AdaptivePoolController` applies its computed
  target again, bounded to `[min_idle, max_conns]` by `MaxConnsLimit`. Unlike the old AIMD this
  cannot be driven by a client: capacity moves only on the controller's 5 s sample, and the
  engine enforces the ceiling structurally. Note the target is still degenerate until
  `mem:findings` A2 is fixed — `Latency()` is always 0 — so in practice it pins to `max_conns`.
- **`EvictIdle`** replaced `clearIdleConns`' acquire-each-and-mark-broken emulation, which
  raced with real callers and was bounded by a stale idle count.
- **`Stat.WaitingAcquires`** is a real gauge, so the dashboard's `current_max_waiters` reports
  callers parked right now instead of the capacity ceiling standing in for it.

## Deliberately dropped

- **The priority wait queue** (`WithPriority`, `waiters[3]`, `maxWaiters`) — it had no callers
  outside the pool package.
- `maintainMinIdle`, `cleanIdle`, `dial`, `shard`, `pooledConn`, `nextPowerOf2` — all now the
  engine's job, or the driver's.

## What it did NOT fix

The swap is below the wire layer. W1 (SCRAM) and W3 (prepared statements) were fixed separately
in `protocol`/`gateway`; **W2 and W4 remain** — the startup handshake is still replayed onto
pooled connections and Terminate is still forwarded to the backend, so Pontus does not yet reuse
a connection across client sessions. W2's *symptom* changed — with dead
connections now correctly evicted, a reused connection is alive, so the re-handshake hangs
instead of returning `invalid frontend message type 0`.

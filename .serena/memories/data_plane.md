# Pontus Data Plane

Everything on the query hot path. Nothing here may allocate per query without a benchmark,
block on I/O while holding a lock, or grow a map keyed by client input.

## `server/proxy/` — Gateway

`gateway.go` is the centre of gravity. `Gateway` holds the handler, balancer, failover
orchestrator, rate limiters, cache manager, the
in-flight collapser map, the middleware chain, and shadow backends.

- `Serve` → accept loop, one `handleClient` goroutine per connection.
- `handleClient` → one 32 KB pooled buffer per **session**, handshake, then the transaction loop.
- `executeRequest` → collapsing, LSN wait, shadowing, state replay, write, `proxyResponse`,
  RTT/error reporting, release-on-idle.
- `reconfigure` rebuilds the chain and every derived config value; `UpdateConfig` holds
  `configMu` while doing it. **The read side does not take `configMu`** — see `mem:findings`.
- `mirrorRequest` does best-effort traffic shadowing to `shadow_backends`.

`middleware/` — `Chain` of `Handle(ctx, *Session, next)`:

| Middleware | Behaviour |
| :--- | :--- |
| `RateLimit` | Global `rate.Limiter` + per-tenant limiters in a `sync.Map` keyed by `State.User`. Cost is estimated from the token stream (JOIN +5, UNION +4, GROUP +3 …), capped at 50. |
| `Firewall` | WAF 2.0: structural token-stream analysis (tautology `OR 1=1`, `UNION SELECT`, `DELETE` with no `WHERE`, access to `pg_shadow`/`pg_authid`/`mysql.user`) + blocked-word list. WAF 3.0: dynamic data masking via `Handler.RewriteQuery`, after which the query is re-normalized and re-classified. |
| `Cache` | Serves from `cache.Manager` on hit, with stale-while-revalidate background refresh; on miss installs a `ResponseCapture` buffer and stores the result with its affected tables. |

`Session` carries client conn, remote addr, `*protocol.SessionState`, backend, server conn,
buffer, normalized query, `QueryInfo`, raw data, replay flag, response capture.

## `server/internal/protocol/` — wire handlers

`Handler` is a composite interface: `QueryClassifier`, `SessionTracker`, `HealthChecker`,
`TopologyDiscoverer`, `ConsistencyManager`, `MetricsCollector`, plus `Handshake`,
`PeekTransactionState`, `Identify`, `Execute`, `RewriteQuery`.

Implementations: `postgres_handler.go`, `mysql_handler.go`. Supporting: `tokenizer.go`
(iterator-based `Tokenize`), `classifier.go`, `session_state.go`, `transaction_state.go`
(`StateIdle` / `StatePartial` / in-transaction), `consistency.go` (LSN capture after a
write, `WaitLSN` before a replica read), `discovery.go`, `metadata.go`.

Invariants: never release a server conn while `TxState != StateIdle`; always replay session
variables and prepared statements after a backend switch; never trust a client length prefix
without bounding it.

The startup phase lives in `postgres_startup.go` and is framed message-by-message: SSLRequest
is declined, then the exchange is relayed **in both directions** until ReadyForQuery, so SCRAM
and md5 work. The transaction loop is still unframed — it assumes one `client.Read` is one
message (`mem:findings` C15).

**Pontus pools** under `auth.mode: pontus` — W2/W4 were fixed 2026-08-09 and eight sequential
clients share one backend connection. This paragraph previously said the opposite and was
stale; `mem:findings` is authoritative when the two disagree. Passthrough still cannot pool,
because the client's startup exchange happens once on one connection.

## `server/internal/pool/` — connection pools

`Server` is a per-backend pool. Since 2026-08-07 the pooling itself — capacity, idle
buckets, reaper, warm-up, statistics — is **gpool's `pooling.Core[*Conn]`**, not ours; see
`mem:pool_engine`. What `Server` still owns is the Pontus-specific half: role, weight, zone,
health and circuit breaker, latency / RTT / error rate / replication lag, draining, and the
gRPC client to the host's `pontus-agent`. The old sharded wait queue, AIMD capacity and
priority waiters are gone.

`AdaptivePoolController` samples the host `system.Monitor` every 5 s and emits throttle state
plus `Suggestion`s that surface in the dashboard's Performance Advisor. It no longer resizes
the pool — capacity is a fixed structural bound owned by the engine, and its BBR computation
was degenerate anyway (`Latency()` is always 0). See `mem:pool_engine`.

`Backend` is the interface the balancer and gateway see (Acquire/Release/health/role/weight/
latency/RTT/error rate/replication lag/draining/stats/DB metrics/version).

`agent_addr` is **mandatory** — `NewServer` errors without it.

## `server/internal/balancer/` — routing

Strategies: `round_robin`, `weighted_round_robin`, `least_conn`, `p2c`, `peak_ewma`,
`consistent` (sticky). All share `CalculateCost` and `FilterNodes` in `balancer.go`.

`CalculateCost` is *designed* as `(0.7·latency + 0.3·RTT) · (activeConns+1) / weight`, then
multiplied by: remote-zone penalty (2×), error-rate penalty above 5%, replication-lag penalty
(linear to `MaxAllowedReplicaLag` = 10 s, then 100×), and slow start (10×→1× over 30 s from
`LastHealthy`).

That cost function **does** run: A2 — `ReportLatency` having no caller, so every backend cost
0 and the ranking strategies degenerated — was fixed 2026-08-17. This paragraph previously
said the penalties were dead and was stale; `mem:findings` is authoritative when the two
disagree.

`FilterNodes` reuses a `sync.Pool` of slices to stay allocation-free. Read-only hints prefer
healthy low-lag replicas, then any healthy replica, then the primary. Writes take exactly one
healthy primary — the cheapest — deliberately, to avoid split-brain.

## `server/internal/cache/` — result cache

`Manager` = `map[string]Item` + `map[table]set[key]` for table-level invalidation, bounded by
`MaxSize` with eviction and a janitor.
`Item` carries value, `Expiration`, `StaleUntil` (= TTL × 2, enabling SWR), and tables.
`Invalidate(tables)` drops every key touching a written table, and the cache middleware calls
it after a successful write. Keys are **not** the query text: `middleware.CacheKey` covers
backend, database, user, every session var and the normalized query, because two sessions
sending identical SQL can legitimately get different rows. The same key namespaces the
in-flight collapser.

## `server/internal/{health,orchestration,consensus}` — HA

- `health/` — `CircuitBreaker` (closed/open/half-open) and the active/passive monitor.
- `orchestration/` — `failover_manager`, `postgres_provisioner`, `agent_client`, Raft glue.
- `consensus/` — `hashicorp/raft` node with an FSM over `sync_backends` / `update_config`
  commands and a `clusterState` snapshot.

The gateway pauses traffic during failover via `failoverMu` + `pauseCond`, then broadcasts.

## The SQL firewall was removed (2026-08-07)

Pontus no longer inspects or rewrites SQL. Deleted: `middleware/firewall.go`,
`pkg/config/firewall.go`, the `firewall` config block, `RecordFirewallViolation`
and the Prometheus counter, the data-masking `RewriteQuery` path on both
protocol handlers, the response-size cap, `SecurityCard` and the Settings
security section.

Proto: `FirewallStats` is gone and `GetStatusResponse` field **14 is reserved**
(name `firewall_stats` reserved too) — do not reuse the tag.

The middleware chain is now **rate limit → cache**. Scope decision, not a
regression: SQL policy is not this product's job. Do not reintroduce it without
asking.

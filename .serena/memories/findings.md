# Pontus Findings

Audited 2026-08-06 (full read + executed reproductions). Partially fixed 2026-08-07.
Items marked **FIXED** have a regression test that fails against the old code.
Everything else is open. None of the open items are precedent to copy.

## Still broken, highest severity first

- **W2 + W4 — the "does not pool" half is STALE (re-measured 2026-08-09).** Eight sequential
  client sessions through the proxy returned the same backend PID *and the same
  `backend_start`*, which is the same physical connection rather than a recycled PID.
  Connections **are** reused across client sessions. The original note below predates the
  gpool migration; do not act on it.

  What this makes live instead: reuse happens with **no identity check**, so a connection
  authenticated as one user is handed to the next client. That is the cross-user data path
  Stage 0 of `docs/design/backend-auth.md` exists to close.

  **Open, and blocking Stage 0:** `PostgresHandler.Handshake` forwards the client's startup
  packet to the server unconditionally (`postgres_handler.go:57`) with no readiness check, so
  a reused connection should receive a StartupMessage while already in the query phase — and
  it demonstrably does not break. Understand that before restructuring the handshake, because
  Stage 0 changes this exact path.

- **W2 + W4 (original note, superseded above). Pontus does not pool.** The client's Terminate is forwarded to the backend, so
  every client session ends its backend connection: `SELECT pg_backend_pid()` over four
  sequential sessions returned 278, 279, 280, 281. And `handleClient` calls `Handshake` on
  every acquired connection, so a *reused* one gets a startup packet it is already past.
  **These are one piece of work** — fixing W4 alone makes connections survive, which is
  exactly what triggers W2. See "Pooling needs Pontus-side client auth" below.

- **A2. `pool.Server.ReportLatency` has zero callers**, so `Backend.Latency()` is always 0.
  `balancer.CalculateCost` starts with `if latency == 0 { return 0 }`, so **every backend
  always costs 0** and the locality, error-rate, replication-lag and slow-start terms below
  that line never execute. `least_conn` / `p2c` / `peak_ewma` all rank by a constant, and
  `FilterNodes`' write path keeps the *first* healthy primary, never the cheapest.

- **B1. [MOOT — the WAF was removed 2026-08-07] Normalization truncation was a WAF bypass.** The firewall inspects `s.Normalized`
  while `executeRequest` sends the raw `s.Data`. Normalization no longer *truncates* on an
  unbalanced quote (fixed in C2), but the underlying split remains: any divergence between
  the inspected text and the executed bytes is a bypass by construction. The durable fix is
  to inspect the bytes that are actually sent.

- **B3. The tokenizer has no comment handling**, so `UNION/**/SELECT` evades the UNION rule.
  No `--`, no `/* */`, no dollar-quoting, no `E'...'`. Every rule matching *adjacent* tokens
  is defeated by an inline comment.

- **B4. `ValidateBackend` is an unauthenticated SSRF / port scanner.** `net.DialTimeout` on a
  caller-supplied address, returning success, latency and the raw `err.Error()`
  (`manager/backend.go:276`). It is on the non-admin allowlist, and with the default empty
  `admin_token` it needs no auth at all.

- **B5–B9. Auth defaults.** Hardcoded JWT secret fallback `"pontus-secret-key"`
  (`manager/auth.go:22`); auth interceptor returns `next(...)` unauthenticated when
  `adminToken == ""` (`middleware/auth.go:35`, `:58`); default `admin`/`admin123`
  (`app.go:80`); `jwt.Parse` without `WithValidMethods`; CORS `AllowedOrigins: ["*"]` over
  the ConnectRPC mux with `Authorization` missing from `AllowedHeaders`.

- **B10/B11. Unbounded maps keyed by client input.** `state.User` is an unauthenticated,
  unbounded string used as the never-evicted tenant-limiter key. `state.Vars` and
  `state.Stmts` have no cap and are replayed on every backend switch.

- **A8 [OPEN, CRITICAL, repro]. A session breaks the moment it changes backend — Pontus
  cannot create a usable backend connection on its own.**

  Corrected 2026-08-09 after measuring rather than reading. The first version of this note
  said "the read/write split never engages"; it does engage, and that is the problem.

  The only startup exchange Pontus ever performs is *proxying the client's own startup
  packet*, in `handleClient` → `Handshake`, onto the one connection acquired for the
  handshake (with `ReadOnly: false`, hence always the primary). Every other pooled
  connection is raw TCP that has never completed a startup exchange. So:

  - query 0 runs on the handshake connection (primary) and succeeds;
  - it is released at the transaction boundary;
  - query 1 routes on its own hint, reaches the replica, gets a freshly dialled socket with
    no startup exchange, and the client session dies — observed as `conn closed`.

  With a single backend this is invisible: the released handshake connection is the one
  handed straight back, so it already carries the exchange. A second backend exposes it on
  the second query. Measured: 10 probes, cache disabled, distinct SQL each — `replica=0
  primary=10` across separate sessions, and `conn closed` from probe 1 onward within one
  session.

  **This is the same root cause as W2 and W4, and as A9 below.** Pontus does not own its
  backend connections: it cannot authenticate as the client, so it cannot open a replacement.
  Everything downstream — read/write splitting, replica routing, the lag gate, the streaming
  gate, cross-client multiplexing — is built on a connection it can only borrow once. The fix
  is Pontus-side backend authentication (pgbouncer's `auth_query` / `auth_file`), which is a
  feature, not a patch. `e2e.TestReadsReachTheReplica` is skipped and will prove it.

  **Contained 2026-08-09, not fixed.** `acquireForSession` now refuses a connection that has
  not completed a startup exchange and falls back to `Session.HomeBackend`, the backend that
  carried this session's handshake. Adding a replica no longer breaks the data plane — it
  simply does not balance, and says so once at WARN naming the finding. Unbalanced beats
  broken; `e2e.TestSessionSurvivesManyQueriesWithAReplica` fails with `conn closed` on query 1
  against the old path.

- **A9 [OPEN, severe, repro]. A pooled server connection is never reset between clients.**
  There is no `DISCARD ALL` anywhere in the tree. `driver.Recyclable` sends only `ROLLBACK`,
  which ends a transaction and nothing else — prepared statements, `SET` variables, temp
  tables, `LISTEN` registrations and session advisory locks all survive it. Worse, it only runs
  when `Dirty()` is true, and `MarkDirty`'s own comment says the normal path never sets it.
  Effect: the first client to run a query succeeds, every later client running the same SQL
  gets `26000 prepared statement "stmtcache_…" does not exist`. Reproduced through the proxy
  and clean directly against PostgreSQL (8/8), single backend, in both transaction and session
  pooling. This breaks pgx, JDBC and asyncpg alike — they all use prepared statements.

  A first attempt at the fix (`DISCARD ALL` on release, gated on a state flag) was **reverted**:
  it exposed a second defect — `ReplaySessionState` does not reapply session variables, so a
  client's own `SET` stopped surviving its own session once the reset actually cleared it. That
  had been masked by connections never being reset. Same family as A8: a connection is handed
  between clients carrying state, because nothing owns its lifecycle. pgbouncer's
  `server_reset_query` is the reference for the reset half.

- **B12. The agent token travels in plaintext by default** — `insecure.NewCredentials()` when
  `tlsConfig == nil`, and one `tls.Config` is shared by the agent client and the DB dialer.
  Still open, and it now matters more: the token is mandatory, so it is always on the wire.

- **B13 [FIXED 2026-08-08]. The agent served every RPC unauthenticated.** The interceptor was
  attached only when a token happened to be set, so `cmd/agent` with no `-token` exposed
  `InstallDatabase`, `PromoteNode` and `RemoveDatabase` — as root — to anyone who could reach
  `:9091`. Now fails closed; `-insecure` is an explicit, warned opt-out. The token comparison
  was also a plain `!=`, which leaks the matching prefix by timing.

- **B14 [FIXED 2026-08-08]. The ExecuteCommand allowlist let a caller own the host.** The
  allowlist existed but contained `cat` and `tail` (arbitrary file read as root → `.pgpass`,
  `admin_dsn`, `/etc/shadow`) and `pontus-agent` itself, which let a caller spawn a second
  agent with `-insecure` on another port and bypass authentication entirely. See
  `agent/infrastructure/allowlist.go` for the criteria a name has to meet.

- **A6 [FIXED 2026-08-08]. Failover never reached the proxy, and could wedge or reverse
  itself.** Three defects between promotion and routing — see `mem:failover`.

- **A7 [FIXED 2026-08-08]. `pkg/service` discarded the error from `onStart`.** Any failed
  startup — port in use, bad config, missing credentials — became a silent hang that systemd
  still reported as active.

- **A3. Client-facing TLS is not wired.** Only `cfg.BackendTLS` reaches `CreateTLSConfig`; the
  proxy listener is a plain `net.Listen`. The `tls:` block is inert.

- **C8. The query-timeout context starts before the client read** (`gateway.go`), so a session
  idle longer than `query_timeout` gets an already-expired context for its next query.

- **C9. Hot-reload data race.** `reconfigure` writes `chain`, `fwConfig`, `shadowBackends`,
  `pauseCond` under `configMu`; `handleClient` reads `g.chain` and `g.fwConfig` with no lock.
  Replacing `pauseCond` strands anyone in `pauseCond.Wait()`.

- **C13. `maxWaiters` per shard** — obsolete, the engine has no wait queue (see
  `mem:pool_engine`). Retained here only so the old note is not mistaken for current.

- **C14. Session pinning is a substring match and never cleared.** `SELECT temp FROM sensors`
  pins the session; nothing sets `Pinned` back to false, so it holds its backend for life.

- **C15. One `client.Read` is assumed to be one whole message** on the query path.
  `extractQueryBytes` still ignores the length prefix. (The *startup* phase is now framed
  correctly — see W1 — but the transaction loop is not.)

- **C16. Blocked words match with `strings.Contains`**, so a column named `dropdown` trips a
  `DROP` rule.

- **C18–C20.** Tenant limiters unbounded at hardcoded 100 rps / 200 burst, and a configured
  `burst < 50` makes `WaitN` fail outright because `estimateCost` caps at 50. The collapser
  buffers whole result sets with no bound. Stale-while-revalidate has no singleflight.

- **D1–D6. Build & ops.** `/go.sum` gitignored and incomplete; CI runs Go tests before the web
  build; `web/src/logs/` was never committed so `bun run build` fails (D1b); no
  `.golangci.yml`; `EnsureUIBuilt()` shells out to bun at server startup; `.goreleaser.yaml`
  uses `archives.format` where v2 wants `formats`.

## Pooling needs Pontus-side client auth (W2 + W4)

Fixing W2 means Pontus answers the client's startup itself, which means it must *verify*
client credentials. Today it relays them to the backend, so it cannot. Direction chosen
2026-08-07: **PgBouncer-style `auth_query`**.

- `backends[].user` / `password` / `database` — a service account per backend. Backend
  connections authenticate once at dial time (pgx `pgconn.ConnectConfig` + `Hijack()`, the
  approach gpool's `examples/gpoolproxy` uses) and are never re-handshaked per client.
- `auth_query` fetches a client's SCRAM verifier from `pg_authid` on demand, cached.
  **The asymmetry matters:** a verifier lets Pontus *check* an inbound client but cannot
  authenticate *outbound* as that user — ClientKey is not derivable from StoredKey. That is
  why a service account is needed as well, and why "just use auth_query" is not sufficient.
- Pontus then runs the SCRAM-SHA-256 server exchange against the client and synthesises
  AuthenticationOk + ParameterStatus (captured from a real backend) + BackendKeyData +
  ReadyForQuery.
- Client identity on a shared connection is restored with `SET ROLE` (a round trip per
  checkout) or by keying sub-pools on (user, database) — the latter is cheaper.

**Do not fix W4 on its own.** It converts a working passthrough into intermittent hangs.

## Fixed 2026-08-07

Each has a regression test in the package it belongs to.

**Wire / handshake**
- **W1.** `Handshake` was a one-way pump — it forwarded server→client and never carried the
  client's reply back, so SCRAM (PostgreSQL's default since 14) and md5 deadlocked. Now
  framed message-by-message and relayed in both directions until ReadyForQuery.
  `server/internal/protocol/postgres_startup.go`.
- **A4.** No `SSLRequest` handling, so a default `sslmode=prefer` client hung. Pontus now
  declines encryption with `'N'` and reads the real StartupMessage.
- **W3.** Prepared statements were replayed onto connections that already held them
  (`42P05`). Two causes: replay was not per-connection, and `TrackPreparedStatement` ran
  *before* the chain so the statement was replayed onto the very connection about to parse
  it. Connections now implement `protocol.StatementHolder`, and tracking moved after the write.
- **B2. [MOOT — the WAF was removed 2026-08-07]** Because tracking ran before the firewall, a query the WAF *blocked* was still stored
  in `state.Vars` / `state.Stmts` and replayed verbatim onto a fresh connection with no
  firewall check. Same move fixes it.

**Cache**
- **A1.** `Invalidate` had zero callers — a cached SELECT served pre-write rows for the whole
  TTL. Writes now invalidate their affected tables. Verified live: read/read/write/read
  returns `before`, `before`, `after`.
- **C1.** Cache and collapser keys were the query text alone. Both now go through
  `middleware.CacheKey`, which is length-prefixed over backend, database, user, all session
  vars (sorted) and the normalized query.
- **C2.** `basicNormalize` collapsed digits *inside identifiers*, so `tenant1_orders` and
  `tenant2_orders` shared a key; and an unbalanced quote silently dropped the rest of the
  statement. Rewritten to tokenize identifiers, quoted identifiers, strings and numbers.
- **C4.** `ClassifyQuery` switched on nine keywords `isKeyword` never emitted, so DDL after a
  SELECT stayed read-only and `INSERT INTO orders` recorded no table. Keyword set extended;
  read-only is now `sawRead && !sawWrite`, so a nested SELECT no longer makes a write look
  like a read.
- **C5.** A stale refresh re-`Set` with nil tables, permanently unreachable by invalidation;
  and `Invalidate` orphaned keys held by more than one table. Both fixed.
- **C6.** `backgroundRefresh` was handed a nil `Backend` and wedged at `cache.go:65`.
- **C7.** `mirrorRequest` and the stale refresh aliased `s.Data`, the live session buffer.
  Both now copy before handing off.
- **A5.** `MaxSize` was parsed and never read; `Cleanup()` had no caller. Bounded with
  eviction and a janitor.
- **C17.** The cache-hit path discarded the error from `s.Client.Write`.

**Pool** — capacity is now a structural bound, acquire/release no longer disagree about
shards, and errors no longer halve capacity. See `mem:pool_engine`.

## Test coverage

`server/proxy` compiled for the first time on 2026-08-07 (both mocks were missing interface
methods). Still no tests at all for `server/internal/{health,consensus}`, `pkg/auth`,
`pkg/config`, `pkg/buffer`, `internal/app`, `server/management/{handler,service,
infrastructure/manager,infrastructure/registry}`, or `agent/` beyond `infrastructure`.

# Pontus Findings

Audited 2026-08-06 (full read + executed reproductions). Partially fixed 2026-08-07.
Items marked **FIXED** have a regression test that fails against the old code.
Everything else is open. None of the open items are precedent to copy.

## Still broken, highest severity first

- **A10 [OPEN, blocks recommending `auth.mode: pontus`, repro]. asyncpg cannot use
  Pontus-side authentication.** pgx and libpq (psql 17.10) both complete a session; asyncpg
  cannot. Its connect hangs, and a *refused* login reports
  `protocol.data_received() call failed` — an asyncpg protocol error, not an authentication
  one. So Pontus emits something during or after the SASL exchange that asyncpg models more
  strictly than the other two.

  Supplying BackendKeyData, which `CompleteClientStartup` had been omitting, did **not** fix
  it — though that omission was a real defect and the fix is kept: PostgreSQL always sends
  it, and it is what a client uses to cancel a query.

  `e2e.TestAsyncpgAuthenticatesAgainstPontus` is skipped with the diagnosis in its comment.
  Next step is to capture asyncpg's traceback and diff Pontus's startup byte stream against a
  real PostgreSQL's for the same client — the difference will be in what follows
  AuthenticationOk.

  This is why a driver matrix exists. Two drivers agreeing proved the exchange
  self-consistent; the third proved it is not yet correct.

- **W2 + W4 [FIXED 2026-08-09, under `auth.mode: pontus`]. Pontus now pools.** Eight
  sequential clients share one backend connection — same PID, same `backend_start` — where
  before, four sessions produced four connections.

  Three things had to be true at once, which is why no single earlier fix moved it: the
  client's Terminate is no longer forwarded (it closed a connection the pool was about to
  reuse); a connection carries the identity it authenticated as, so it is only offered to the
  same user and database; and a reused connection is not given a second startup exchange —
  the backend is past that phase and reads a StartupMessage as a malformed command.

  **Reuse is a property of Pontus-side authentication, not of the release path.** Under
  passthrough the connection carries the client's own startup exchange and there is no way to
  run one for somebody else, so passthrough still gives the connection up at session end.
  Scoping this wrongly broke the extended protocol in passthrough and the E2E suite caught it.

  A pooled connection is reset with `DISCARD ALL` on every release, so no prepared statement,
  SET variable or temp table crosses between clients. Consequence worth knowing: under
  transaction pooling a client's own `SET` does **not** survive its next transaction boundary.
  That is the documented semantics of transaction pooling and pgbouncer behaves identically —
  an earlier attempt at this reset was reverted after misreading that as a regression.

- **W2 + W4 (original note). Pontus does not pool.** *Re-confirmed 2026-08-09:* three sequential client
  sessions returned three different backend PIDs **and three different `backend_start`s**,
  and the server log shows a connect/authorize/disconnect per session. Connections are not
  reused across clients.

  A probe on 2026-08-09 briefly appeared to show the opposite — the same PID and
  `backend_start` eight times — and that reading was **wrong**: the harness config has the
  result cache enabled and the probe asked the identical question each time, so it was
  reading a cached row, not the backend. Any probe of connection identity must disable the
  cache and vary the SQL. The client's Terminate is forwarded to the backend, so
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

- **A8 [FIXED 2026-08-09, under `auth.mode: pontus`]. Reads reach replicas.**
  `e2e.TestReadsReachTheReplica` is un-skipped and passing.

  The cause was that Pontus could only ever produce the client's own startup exchange,
  forwarded once, so every other connection was a raw socket a session could not speak on.
  Holding the ClientKey recovered from the client's SCRAM proof removes that: a mid-session
  acquisition that lands on a fresh connection now authenticates it as the same user rather
  than falling back to the handshake backend.

  One more thing was needed and is easy to miss: the startup connection is acquired with a
  **write** hint, because nothing is known about the session yet. Keeping it meant every
  session's first statement ran on the primary whatever it asked for — and a client that
  issues one read per connection never touched a replica at all. It is now returned to the
  pool once the client's startup completes, so the first statement routes on its own hint.

  Under passthrough this connection is the session's only way to reach a backend, so the
  release and the mid-session authentication are both conditional on Pontus-side auth.

- **A9 [FIXED 2026-08-09]. The result cache answered extended-protocol messages.**

  Diagnosis corrected. The first version of this note blamed connection reuse and a missing
  `DISCARD ALL`; connections are not reused across clients (see W2 above), and the reverted
  reset was aimed at the wrong thing.

  PostgreSQL's extended protocol splits one query into Parse, Bind and Execute. The reply to
  a Parse is ParseComplete — not a result set. But a Parse carries the SQL text, so it
  normalised and classified as a cacheable read, and the second client to run the same
  statement was answered with the first client's stored *result* where its ParseComplete
  belonged. The connection desynchronised and the client's next Bind referenced a statement
  the server had never parsed: `26000 prepared statement "stmtcache_…" does not exist`.

  This broke pgx, JDBC and asyncpg — every mainstream driver prepares by default. The first
  client always succeeded, so a single client or a single run looked healthy.

  Fixed by `QueryClassifier.Cacheable`: PostgreSQL allows only `'Q'`, MySQL only `COM_QUERY`.
  Caching still works for the simple protocol — the regression test asserts hits as well as
  correctness. `e2e.TestPreparedStatementsWorkWithTheCacheEnabled` fails with 26000 against
  the old path.

  **Method note:** cache-on reproduced it 14/15; cache-off was 8/8 clean. Toggling the cache
  is the first thing to try on any "it works once then stops" symptom here.

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

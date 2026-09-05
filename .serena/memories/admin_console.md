# The pgbouncer-compatible administration console

Added 2026-09-04 (`feat/pgbouncer-admin-console`). A virtual database on the
**proxy** port that answers pgbouncer's SHOW commands from Pontus's own state,
never touching a backend — which is what makes it useful during exactly the
incident where the database is unreachable.

It exists for migration, not novelty: a deployment replacing pgbouncer already
has exporters scraping `SHOW POOLS`, dashboards built on its column names and
runbooks naming `SHOW DATABASES`. See `mem:core` for where this sits in the
competitive picture.

## Where it lives

| Concern | File |
| :--- | :--- |
| Config, authorisation rule, startup validation | `pkg/config/admin_console.go` |
| Session loop, both protocols, the SHOW handlers | `server/proxy/admin_console.go` |
| Result-set encoding, format codes, Parse/Bind decoding | `server/internal/protocol/result_set.go` |
| Bounded message reader | `server/internal/protocol/command_reader.go` |
| Live client sessions | `server/proxy/session_registry.go` |
| Per-identity pool occupancy | `server/internal/pool/pool_stat.go` |

Wired in `openSession` **after authentication and before acquisition**, returning
`errAdminHandled` so `handleClient` knows the connection never held a backend —
the same shape as `errCancelHandled`.

## Invariants that must not regress

- **The console requires `auth.mode: pontus`.** In passthrough a *backend*
  verifies the password and the console has no backend to ask, so it refuses.
  The check is a closure over `g.credentials`, asked at session time: the
  credential store is installed *after* the gateway is built, so a value
  captured in the constructor is false for every session.
- **`admin_console.users` has no default and no wildcard.** Enabled with nobody
  listed fails startup (`ErrAdminConsoleNoUsers`). Matching is exact — folding
  case would let `Admin` reach a console configured for `admin`.
- **Authentication and authorisation are answered differently.** A role that
  authenticated but is not listed is told so (42501), not disconnected as if its
  password were wrong.
- `Users` is read only by `Permits`/`Validate`, so the rule has one home. That
  is why it is in `allowedUnwiredNested` in `pkg/config/wired_test.go` — the
  source scan cannot see a consumer inside the package.

## Both query protocols, and why

pgx, the JDBC driver and most client libraries default to Parse/Bind/Execute. A
console that spoke only the simple protocol worked when a person tried it with
psql and failed from every program — found by the e2e test, not by review.

`Bind`'s **result format codes are honoured**. A driver decodes by what it
requested, not by what RowDescription declared, and pgx asks for binary for every
type it has a binary codec for; sending an int8 column as text made it fail with
`invalid length for int8`. `ResultSet` therefore stores rows as values and
encodes at send time, because the client chooses the encoding after the rows
exist. After an error the loop skips to `Sync`, as the protocol requires.

## Deliberately not implemented

`SHOW STATS` and `SHOW SERVERS` need per-database query/byte totals and
per-connection server detail, which Pontus does not keep. They return an error
naming the reason. **Do not "fix" them by reporting zeros** — that puts
"0 queries/sec" on a dashboard forever and looks like a working integration.

Columns naming a state Pontus does not have (`sv_tested`, `sv_login`, `sv_used`)
are zero, which is accurate rather than a placeholder.

## SHOW POOLS reports per identity

Pools are keyed `(backend, database, user)` (`mem:pool_engine`), so occupancy is
reported that way rather than summed: a backend can sit at half its ceiling while
one tenant's pool is full and all its sessions queue. `PoolStat.AverageWait` is
pgbouncer's `avg_wait_time` — the figure that says a pool is too small, which
occupancy alone never does. It was already in gpool's `Stat` and previously
unexposed.

`PoolStats()` is an **optional interface** asserted at the call site, not a
`Backend` method: that interface is already far past the size `.junie/guidelines.md`
allows, and every mock would grow a method none of them need.

## Still open, in priority order

Gaps against pgbouncer/pgcat identified alongside this work, none started:

1. ~~**No `[databases]` equivalent**~~ — **done 2026-09-06**, see
   `mem:database_routing`. What remains of pgbouncer's `[databases]`: no
   `force_user`, no per-database `pool_mode`, and no `max_db_connections`
   (a total across users for one database, distinct from the per-identity
   `max_conns` that shipped).
2. **No session pooling mode** — `pooling_mode` is transaction|statement only.
3. **Zero benchmarks in the tree**, so `AGENTS.md`'s "no per-query allocation
   without a benchmark" veto is unenforceable and the README's performance
   claims are unbacked.
4. **No container or Kubernetes story** — no Dockerfile, Helm chart or manifests.
5. **No zero-downtime binary upgrade** (pgbouncer's `-R` takeover).
6. **The README still advertises the removed WAF** ("WAF 2.0", "Exfiltration
   Guard", and a `# Security (WAF)` config block that no longer parses).

Sharding was considered and rejected as a direction: it fights the cache and the
LSN-consistency logic, and the cache is the better differentiator.

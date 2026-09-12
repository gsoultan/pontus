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
2. ~~**No session pooling mode**~~ — **wrong, it exists.** `poolSession` is
   implemented in `server/proxy/pooling_mode.go` with seven unit tests and e2e
   coverage in `outage_test.go` and `limits_test.go`. The claim came from a
   stale comment on `config.Options.PoolingMode` saying "transaction or
   statement", now corrected. Check the code, not the comment.
3. ~~**Zero benchmarks in the tree**~~ — **done 2026-09-10.** See
   `mem:data_plane`. Writing the first one immediately found the tokenizer
   allocating 15-40 times per query against a README claiming zero.
4. **No container or Kubernetes story** — no Dockerfile, Helm chart or manifests.
5. ~~**No zero-downtime binary upgrade**~~ — **done 2026-09-11**, see
   `mem:zero_downtime_upgrade`. `reuse_port` plus an orchestration lock.
6. ~~**The README still advertises the removed WAF**~~ — **done 2026-09-10.**
   The claim and the dead config block are gone, and the performance line now
   names the command that produces its numbers.

Found or sharpened since:

7. ~~**`SHOW STATS`**~~ — **done 2026-09-10.** Answered from
   `observability.DatabaseRegistry`: a readable copy of the counters, because a
   Prometheus CounterVec is write-only from the process's point of view.
   A session resolves its bucket **once** and holds the pointer, so recording is
   a few atomic adds with no map lookup and no lock on the query path. Bounded
   at 256 databases — the key is a client-supplied name — with an `(other)`
   bucket past it so totals stay right while attribution stops.
   `total_wait_time` comes from the pools, since gpool already measures it and a
   second measurement would be one more thing to disagree.
   **`SHOW SERVERS` is done too, 2026-09-11** — and the reason it stayed open was
   a wrong claim of mine. gpool reports occupancy, which is the right contract
   for it; but Pontus owns `pool.Conn` precisely so it can keep per-connection
   state, and it already carried connect time, use count, identity, readiness
   and socket failure. Only a registry of the live ones was missing
   (`server/internal/pool/conn_registry.go`), maintained in the driver's
   `Connect`/`Close` so it cannot drift from what is really open. `Conn.busy` is
   the one addition — the engine's `handle` may only be touched by the goroutine
   owning the checkout, so it cannot answer "active" for an observer.

   **Every command the console advertises is now implemented.**

   pgbouncer's `ptr`, `link` and `remote_pid` are omitted rather than faked:
   they identify a connection inside pgbouncer's own structures.
8. **`VacuumDatabase` is not on the `AgentClient` interface**, so the agent
   implements it and the proxy cannot call it.
9. ~~**The agent token crosses the network in cleartext by default**~~ —
   **fixed 2026-09-10**, see `mem:security`. Both ends now refuse a
   non-loopback agent without TLS.
10. **`internal/app` and `server/management/service` have no test files at all.**
    ~~`server/internal/consensus`~~ — tested 2026-09-11, and the tests found four
    defects plus the fact that it is not wired to anything (`mem:consensus`).

Sharding was considered and rejected as a direction: it fights the cache and the
LSN-consistency logic, and the cache is the better differentiator.

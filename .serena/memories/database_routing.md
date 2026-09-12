# Per-database routing and limits (`databases:`)

Added 2026-09-06 (`feat/pgbouncer-admin-console`). Pontus's answer to pgbouncer's
`[databases]` section: an entry may rename a database, bound it, or both.

```yaml
databases:
  - name: app            # what the client connects to
    database: app_prod   # the real name on the backend; empty means name
    max_conns: 20        # per-identity ceiling; zero takes the global max_conns
  - name: "*"            # fallback: limits only, never a rewrite
    max_conns: 5
```

## Where it lives

| Concern | File |
| :--- | :--- |
| `Database`, `Databases`, `Route`, `Resolve`, `Limit`, `Validate` | `pkg/config/database.go` |
| Alias applied once at session open | `resolveDatabase` in `server/proxy/open_session.go` |
| Startup packet rewrite | `server/internal/protocol/startup_rewrite.go` |
| Per-identity ceilings, the cap-not-target rule | `server/internal/pool/database_limits.go` |
| Identity-aware pool construction | `newCore(identity)` in `pool_set.go`, applied in `server.go` |

## Decisions that must not regress

- **An unlisted database resolves to itself.** `databases:` is deliberately not
  an allowlist: making it one would mean enumerating every database in a
  deployment before a limit could be set on one of them.
- **The alias rewrites the raw startup packet, not just the parsed field.**
  Passthrough forwards the client's own packet to the backend, so rewriting only
  `req.Database` would key the pool by the alias while the connection opened the
  name the client sent. Those two disagreeing about which database a connection
  belongs to is the shape of finding A11. Resolution happens **once**, at session
  open, before the pool key, the backend startup or `SetIdentity` can see it.
- **`max_conns` is per identity**, matching pgbouncer's per-database `pool_size`.
  A connection carries the credentials it authenticated with, so `(database, user)`
  is the unit a pool is keyed by (`mem:pool_engine`) and therefore the unit a
  ceiling applies to.
- **The rule is a cap, not a target.** `Server.SetMaxConns` resizes per identity
  via `poolSet.eachIdentity` and applies `ceilingFor(target, rule)` — the lower of
  the two. The adaptive controller must still be able to reclaim connections under
  pressure; a per-database value that overrode it would let one tenant keep what
  the controller is trying to take back. `MaxConnsLimit` moves with `MaxConns` at
  pool creation so the controller cannot raise a bounded database back up.
- **`"*"` carries limits and never rewrites**, and a wildcard with `database:` set
  is refused at startup: pointing every unlisted name at one real database sends
  one tenant's queries to another tenant's data. A duplicate name is refused too —
  it would otherwise resolve to whichever entry the loop reached first.
- Pools appear in `SHOW POOLS` (`mem:admin_console`) under the **real** database
  name, because that is what the connections were opened against.

## Not covered

Still missing from pgbouncer's `[databases]`: `force_user`, a per-database
`pool_mode` (would need the runtime pooling mode to become per-session), and
`max_db_connections` — a *total* across users for one database, which is a
different bound from the per-identity `max_conns` that shipped. `poolSet.maxTotal`
already bounds a backend across every identity, so the gap is per-database
totals specifically.

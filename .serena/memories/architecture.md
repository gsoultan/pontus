# Pontus Architecture

Pontus is a database connection pooler, proxy and load balancer for PostgreSQL and
MySQL/MariaDB — Go 1.26 backend plus an embedded React 19 dashboard, shipped as a single
CGO-free binary. Module `github.com/gsoultan/pontus`, ~32k LOC Go.

The design constraint that explains every rule: **every byte on the data path came from a
client you do not trust, and every connection you hand out is a connection a real workload
is blocked on.**

## Three shipped binaries

| Binary | Entry | Role |
| :--- | :--- | :--- |
| `pontus` | `cmd/pontus` | The proxy server: data plane + management API + dashboard |
| `pontus-agent` | `cmd/agent` | Sidecar on each database host — system metrics, config validation, provisioning |
| `pontusctl` | `cmd/pontusctl` | CLI for status, backend management, log tailing, replica provisioning |

All three install as OS services via `kardianos/service` (`pkg/service/`).

## Two planes

**Data plane** — `server/proxy/` over `server/internal/{protocol,pool,balancer,cache,health,orchestration,consensus}`.
One goroutine per client connection, a middleware chain per query. See `mem:data_plane`.

**Control plane** — `server/management/` (ConnectRPC, gRPC-compatible), `web/` (dashboard,
embedded via `//go:embed all:dist`), `agent/` (go-kit sidecar), `pkg/observability/`.
See `mem:control_plane`.

`internal/app/` is the wiring layer that owns both: config load, SQLite store init, legacy
JSON→SQLite migrations, password hashing migration, the management HTTP server, and the
service lifecycle.

## Request lifecycle (the path to understand first)

1. `Gateway.Serve` accepts a client conn, spawns `handleClient` (`wg.Go`).
2. A 32 KB buffer is taken from `pkg/buffer` **once per session**, not per query.
3. A backend is acquired for the handshake; `protocol.Handler.Handshake` bridges client↔server auth.
4. Transaction loop: read from client → `ClassifyQuery` + `NormalizeQuery` → run the
   middleware chain → `executeRequest`.
5. Chain order is **rate limit → firewall → cache**, built in `Gateway.reconfigure`.
6. `executeRequest` does request collapsing for idempotent reads, LSN-consistency wait when
   reading from a replica, optional traffic shadowing, session-state and prepared-statement
   replay after a backend switch, then proxies the response.
7. When `TxState` returns to `StateIdle` the server connection is released back to the pool.
   That is transaction-mode pooling — the whole point of the project.

## Layering convention

`transport → endpoint → service → infrastructure → store`.
`service/` holds interfaces, `infrastructure/manager/` holds implementations, `store/` holds
SQLite. A handler reaching a store is a layer skip and is vetoed.

## Key dependencies (and why)

- `connectrpc.com/connect` — management API, gRPC-compatible without a gRPC server.
- `pingcap/tidb/pkg/parser` — real SQL parsing for classification and masking.
- `jackc/pgx/v5`, `go-sql-driver/mysql` — protocol knowledge and the example clients.
- `hashicorp/raft` — failover consensus (`server/internal/consensus`).
- `modernc.org/sqlite` — **pure-Go** SQLite; load-bearing for `CGO_ENABLED=0`.
- `shirou/gopsutil/v4` — host metrics for the adaptive pool controller and the agent.
- `go-kit/kit` — the agent's endpoint/transport structure.

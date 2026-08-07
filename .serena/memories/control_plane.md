# Pontus Control Plane

Management API, dashboard, agent and observability. Nothing here may sit on the query path.

## `server/management/` — ConnectRPC API

Layering, strictly: `handler/` (ConnectRPC transport) → `endpoints.go` (go-kit style
endpoint set) → `service/` (interfaces, one per domain) → `infrastructure/manager/`
(implementations) → `store/` (SQLite).

Domains, each an interface file in `service/` with a matching manager: `auth`, `backend`,
`cluster`, `info`, `observability`, `project`, `proxy`, `setting`.
`infrastructure/registry/` is the composition root the managers resolve through;
`infrastructure/state/` holds live project and proxy state.

Served at `mgmt_addr` (default `:9090`) over h2c, alongside `/metrics` (Prometheus) and
`/` (the embedded dashboard). Endpoint prefix: **`/api.proto.service.ManagementService/`**
(`ManagementServiceName` in `api/proto/service/serviceconnect/management.connect.go`) —
note the `service` segment; the mux falls through to the dashboard handler, so a wrong
prefix returns the SPA or a 502 from the Vite proxy rather than a 404.

**Auth** — `middleware/auth.go` is a `connect.Interceptor`:
- `/Login` is always unauthenticated.
- A legacy static `admin_token` is accepted verbatim.
- Otherwise an HS256 JWT is parsed; non-`admin` roles are restricted to a hardcoded
  read-only allowlist (`GetStatus`, `ListProjects`, `GetMetricsHistory`,
  `GetTopQueriesHistory`, `GetLogs`, `GetServerInfo`, `ValidateBackend`).
- **Every new RPC is admin-only by default** — adding one to the allowlist is a deliberate
  security decision, not a formality.

Tokens are minted in `infrastructure/manager/auth.go` with a 24 h `exp`. Passwords are
bcrypt via `pkg/auth`. See `mem:security` for what is wrong with the current defaults.

## `web/` — dashboard

React 19 + Mantine v9 + TanStack Router + TanStack Query + Zustand, built by Vite/Bun,
embedded through `web/ui.go` (`//go:embed all:dist`).

Conventions that must hold: every route has a `routes/<name>.lazy.tsx` counterpart (code
splitting is enforced by convention, not by a bundler check); one component per file, named
after the file; one hook per file, colocated with its domain (`projects/useProjects.ts`);
TanStack Query lives inside hooks with stable `queryKey` arrays.

Views: status/index, backends, metrics, logs, queries, projects (+ proxy create/edit),
login. Charts via `@mantine/charts`/`recharts`, topology via `@xyflow/react`.

**Hard rule:** captured SQL and log lines are hostile client input. Never render them as
HTML or markdown — that turns the observability surface into stored XSS.

Dev loop: `bun run --cwd web dev`, then run Pontus with `PONTUS_DEV=true`; the server detects
the Vite dev server and proxies to it.

## `agent/` — per-host sidecar

go-kit structure: `transport/grpc.go` → `endpoint/endpoints.go` → `services/` →
`infrastructure/`. Collects system metrics (`monitor`), validates database configuration
(`infrastructure/validator/postgres.go`), manages the DB service (`service.go`), and can
install packages for replica provisioning (`apt.go`).

Authenticated with `agent_token`; the pool's gRPC client attaches it as metadata.
An agent RPC must never build a shell command from a request field.

## `pkg/observability/` — telemetry

- `metrics.go` — Prometheus collectors (`QueriesTotal`, …).
- `tracing.go` — OpenTelemetry spans (`StartSpan`).
- `logs.go` — `BroadcastHandler` wraps the default `slog` handler so logs fan out to the
  dashboard live stream and into `store/log_store.go`.
- `tracker.go` — top-queries tracking and history, persisted via `store/metric_store.go`.
- `system.go` / `throttler.go` — host metrics reporting and emission throttling.
- Both stores auto-prune hourly with 7-day retention, started from `internal/app/app.go`.

**Cardinality rule:** query text, client address and username are never Prometheus labels.

## Storage

SQLite (`modernc.org/sqlite`, pure Go) in three files under the data dir:
`management.db` (projects, proxies, users, settings), `logs.db`, `metrics.db`.
Paths resolved by `pkg/system/path.go`.

`internal/app/` runs one-time migrations at startup: legacy `projects.json` / `users.json`
→ SQLite (originals renamed `.bak`), raw passwords → bcrypt, outdated project shapes, and
`config.yaml` → store when the store is empty.

## `server/internal/insights/` — AI-driven advice

`Engine` keeps per-fingerprint `Insight`s. `postgres.go` runs `EXPLAIN` through a
`QueryExplainer` and turns plans into `Recommendation`s (nested loops, seq scans);
`tuner.go` derives server-tuning suggestions from host RAM and expected connections.

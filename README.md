# Pontus

Pontus is a high-performance, cloud-native database connection pooler and load balancer written in Go 1.27. It is designed to scale database workloads by managing connection pools efficiently, providing advanced observability, and ensuring high availability.

## Features
- **Multi-Protocol Support**: PostgreSQL and MySQL/MariaDB with robust SQL parsing.
- **Transaction Mode Pooling**: Minimizes server connections by releasing them when idle.
- **Load Balancing**: Round Robin, Least Connections, and Consistent Hashing (Sticky).
- **Failover & Health Checks**: Passive and active health monitoring with Raft-driven consensus.
- **Security**: client authentication (scram-sha-256 / md5), TLS on both sides, and per-tenant rate limiting.
- **Intelligent Caching**: Semantic result-set cache with automatic table-level invalidation.
- **AI-Driven Insights**: Proactive query plan analysis (EXPLAIN) and optimization suggestions.
- **Advanced Observability**: Real-time Top Queries dashboard and OpenTelemetry tracing.
- **`pontusctl` CLI**: Dedicated CLI tool for management and status.
- **Web Dashboard**: Modern, built-in dashboard for monitoring (embedded in binary).
- **Adaptive Pooling**: BBR-style congestion control for DB connections with resource-aware throttling.
- **Performance Advisor**: Real-time suggestions for system tuning based on CPU, memory, and concurrency.
- **Low Footprint**: the query path allocates nothing to tokenize or route a
  statement. Measured, not asserted — `go test ./server/internal/... -bench .
  -benchmem` reports 0 allocs/op for `Tokenize`, `CalculateCost` and
  `FilterNodes`. There is no published throughput figure yet; when there is one
  it will come with the command that produced it.

---

## Architecture

Pontus consists of three main components:

1.  **Pontus Server**: The core proxy that handles client connections, manages backend pools, and provides the management API and Web Dashboard.
2.  **Pontus Agent**: A lightweight monitoring agent that runs alongside your database nodes to collect system-level metrics and validate configurations.
3.  **pontusctl**: A powerful command-line interface for managing and monitoring your Pontus cluster.

---

## Installation

### Pontus Server

The Pontus Server can be installed as a standalone binary or as a system service.

#### Prerequisites
- **Go 1.27+**
- **Bun** (required for building the Web Dashboard)

#### Building from Source
To build the server with the embedded Web Dashboard:
```bash
# 1. Generate code and build UI assets
go generate ./cmd/pontus

# 2. Build the binary
go build -o pontus ./cmd/pontus
```

#### Running
```bash
./pontus -config config.yaml
```

#### Installing as a Service (Windows/Linux)
Pontus can be installed as a background service:
```bash
# Install the service
./pontus -service install -config C:\path\to\config.yaml

# Start the service
./pontus -service start

# Other commands: stop, uninstall, restart, status
```

---

### Pontus Agent

The Agent should be installed on every database node you wish to monitor. It provides deep visibility into the host system and database instance.

#### Building from Source
```bash
go build -o pontus-agent ./cmd/agent
```

#### Running
```bash
./pontus-agent -addr :9091
```

#### Installing as a Service (Windows/Linux)
```bash
# Install the agent service
./pontus-agent -service install -addr :9091

# Start the agent service
./pontus-agent -service start

# Other commands: stop, uninstall, status
```

---

### pontusctl CLI

`pontusctl` is the primary tool for administrative tasks and real-time monitoring via the command line.

#### Building from Source
```bash
go build -o pontusctl ./cmd/pontusctl
```

#### Basic Commands
```bash
# Check status of all backends
./pontusctl status

# Add a new backend
./pontusctl add-backend 127.0.0.1:5433 replica

# Tail real-time logs
./pontusctl logs info

# Provision a new replica (automated)
./pontusctl provision-replica <source_addr> <target_addr> <user> <password>
```

---

## Configuration

Pontus uses a YAML configuration file. Below is a comprehensive example with the most common options:

```yaml
# Proxy settings
proxy_addr: ":5432"           # Address to listen for database client connections
mgmt_addr: ":9090"            # Address for management API and Web Dashboard
protocol: "postgres"          # Protocol: "postgres" or "mysql"
pooling_mode: "transaction"   # "transaction" or "statement"
balancer: "least_conns"       # "round_robin", "least_conns", or "sticky"

# Connection settings
dial_timeout: 5s              # Timeout for connecting to backends
max_conns: 100                # Maximum connections per backend
min_idle: 10                  # Minimum idle connections to keep in pool
health_interval: 10s          # Interval for active health checks

# Backend servers
backends:
  - addr: "127.0.0.1:5433"
    agent_addr: "127.0.0.1:9091" # Link to Pontus Agent on this host
    role: "primary"           # "primary" or "replica"
    weight: 10                # For weighted load balancing
    zone: "us-east-1a"
  - addr: "127.0.0.1:5434"
    agent_addr: "127.0.0.1:9092"
    role: "replica"
    weight: 10
    zone: "us-east-1b"

# Rate Limiting
rate_limit:
  enabled: true
  rps: 1000                   # Requests per second
  burst: 50                   # Allowed burst size

# Query Caching
cache:
  enabled: true
  ttl: 1m                     # Time-to-live for cached results
  max_size: 1024              # Maximum number of cached items

# TLS Configuration
tls:
  cert_file: "server.crt"
  key_file: "server.key"

# Per-database routing and limits (pgbouncer's [databases])
#
# Optional. Without it every database resolves to itself under the global
# max_conns, which means the ceiling a busy tenant needs is the ceiling every
# other tenant also gets.
databases:
  - name: app                 # what the client connects to
    database: app_prod        # the real name on the backend (optional)
    max_conns: 20             # per-identity ceiling for this database (optional)
  - name: "*"                 # fallback: limits only, never a rewrite
    max_conns: 5

# Failover and recovery
failover:
  enabled: false              # automatic promotion; split-brain resolution runs regardless
  failure_threshold: 3        # consecutive checks with no healthy primary before promoting
  follow_primary: true        # re-point surviving replicas after a promotion
  max_replica_lag: 10s        # reads stop going to a replica past this
  auto_reattach: true         # pull a non-streaming replica out of the read pool
  auto_rejoin: false          # and rebuild it as a replica of the current primary
  auto_rejoin_interval: 5m
  auto_rejoin_timeout: 30m    # a rebuild can mean a base backup of the whole cluster
  auto_rejoin_max_attempts: 3

# pgbouncer-compatible administration console
#
# A virtual database on the proxy port that answers SHOW commands about Pontus
# itself, so the exporters, dashboards and runbooks a deployment already has
# keep working after Pontus replaces pgbouncer.
admin_console:
  enabled: false              # off by default; it reports pool and backend inventory
  database: pgbouncer         # the database name a client connects to
  users:                      # roles allowed in — no default, and no wildcard
    - admin
```

### Automatic recovery after a failover

`auto_reattach` and `auto_rejoin` are two halves of the same problem.

A former primary that comes back after a failover is **up, answers queries, and
will never stream again** — it is on an abandoned timeline. `auto_reattach`
(on by default) stops routing reads to it, so it cannot serve stale rows. But
nothing then *fixes* it, and the cluster runs permanently short until an
operator notices.

`auto_rejoin` closes that: a node that is reachable but no longer replicating is
rebuilt as a replica of the current primary, retried on an interval and given a
bounded number of attempts before it is left to a person.

```yaml
failover:
  enabled: true
  auto_rejoin: true
```

- **Off by default.** A rebuild can mean a `pg_basebackup` that discards the
  node's data directory, which is not something to start underneath an operator
  who has not asked for it — the same reason `enabled` is off.
- **Only reachable nodes are rebuilt.** A node Pontus cannot reach might simply
  be rebooting; rebuilding it is impossible anyway, since the rebuild runs
  through its agent.
- **The write role is never moved.** A rebuilt node returns as a replica.
  Returning the write role to a preferred node causes a *second* unplanned
  outage, so it stays a deliberate operator action — Patroni and pgpool-II make
  the same call.
- Watch `pontus_auto_rejoin_pending` (nodes reachable but serving nothing) and
  `pontus_auto_rejoin_total{result="exhausted"}` (Pontus has given up and the
  node needs a person).

#### Agent transport security

**Pontus refuses an unencrypted agent that is not on this host**, on both ends.

The agent token is a bearer credential for an interface that rebuilds nodes,
takes backups and deletes data directories, as root. Without TLS it is on the
wire on every call. This used to be a warning; a warning in a log nobody reads
is not a control.

| Situation | Result |
| :--- | :--- |
| `agent_tls` configured, agent started with `-tls-cert`/`-tls-key` | works |
| Agent on loopback (`127.0.0.1`, `localhost`, `::1`) | works — the token never leaves the machine |
| Agent on another host, no TLS | **refused**, on both ends |

To accept the risk deliberately — a trusted private network, say — set
`agent_allow_cleartext: true` in the config and start the agent with
`-insecure`. Both are needed: the proxy refuses to dial and the agent refuses to
serve, independently.

> **Upgrading:** a multi-host deployment that has never configured `agent_tls`
> will now fail to reach its agents. That is the point — the credential has been
> crossing the network in cleartext — but it is a behaviour change, and the
> error names both ways forward.

#### Agent configuration

The agent manages one cluster on its host. Tell it which, and which role its
tools connect as:

```bash
pontus-agent -token "$PONTUS_AGENT_TOKEN" \
  -data-dir /var/lib/postgresql/17/main \
  -db-user postgres
```

Both have defaults — a scan of the usual locations, and `postgres` — and both
are worth stating. A scan finds *a* cluster, which on a host running two is the
wrong one, and a rebuild erases whatever it is pointed at.

The agent connects over the cluster's own unix socket with no password. That is
not a shortcut: it runs on the database host as root, so it can become the
cluster's owner, and that account authenticates locally by peer or trust.
Shipping it a password would add a secret to the wire and buy nothing.

**The agent must outlive the database.** A rebuild stops PostgreSQL, so where
the database is PID 1 — a database-in-a-container deployment — stopping it
takes the agent down mid-rebuild. Pontus refuses that up front rather than
starting what it cannot finish. Run the agent as its own service, which is the
ordinary VM or systemd shape.

Two settings matter for a rebuild and are worth stating explicitly:

```yaml
backends:
  - addr: "10.0.0.5:5432"
    # Where this node's cluster lives. A rebuild erases a data directory, so
    # this is the last place a guess belongs — without it Pontus asks the
    # server, and the agent falls back to scanning the usual locations, which
    # finds the wrong cluster on a host running two.
    data_dir: /var/lib/postgresql/17/main
    # How *other database nodes* reach this one, when that differs from how the
    # proxy does. The rebuild runs pg_basebackup on the node being rebuilt, so
    # it is that node's view that matters. Empty means "same as addr", which is
    # correct on a flat network. Patroni calls this connect_address.
    peer_addr: "10.0.0.5:5432"
```

### Per-database routing

`databases:` is Pontus's `[databases]`. Each entry may rename a database, bound
it, or both:

| Field | Meaning |
| :--- | :--- |
| `name` | the name the client puts in its startup packet |
| `database` | the real name to open on the backend; empty means `name` |
| `max_conns` | per-identity ceiling for this database; zero takes the global `max_conns` |

```yaml
databases:
  - name: app
    database: app_prod   # a cutover moves this without touching the application
    max_conns: 20
  - name: reporting
    max_conns: 2         # bound one tenant without bounding everyone
```

- **An unlisted database resolves to itself**, under the global `max_conns`.
  This is not an allowlist — making it one would mean enumerating every database
  in a deployment before a limit could be set on one of them.
- **`max_conns` is per identity**, matching pgbouncer's per-database `pool_size`.
  A connection carries the credentials it authenticated with, so `(database, user)`
  is the unit a pool is keyed by and therefore the unit a ceiling applies to.
- **The rule is a cap, not a target.** The adaptive controller may lower capacity
  for the whole backend under pressure; the effective ceiling is the lower of the
  two, so the controller can still reclaim connections.
- **`"*"` carries limits and never rewrites.** Pointing every unlisted name at
  one real database would send one tenant's queries to another tenant's data, so
  a wildcard with `database:` set is refused at startup.
- Aliasing works in both `passthrough` and `pontus` auth modes: the client's
  startup packet is rewritten so the pool key, the backend connection and the
  identity recorded for reuse all name the same database.

Pools appear in `SHOW POOLS` under the **real** database name, because that is
what the connections were opened against.

### The administration console

With `admin_console.enabled: true`, connect to the `pgbouncer` database on the
**proxy** port and run pgbouncer's commands:

```bash
psql -h pontus-host -p 5432 -U admin -d pgbouncer -c "SHOW POOLS"
```

| Command | Reports |
| :--- | :--- |
| `SHOW POOLS` | occupancy per `(database, user)` — Pontus's pools are keyed that way |
| `SHOW DATABASES` | one row per configured backend, with its role and ceiling |
| `SHOW CLIENTS` | live client sessions |
| `SHOW LISTS` | the size of each internal collection |
| `SHOW CONFIG` | the settings governing the data path |
| `SHOW VERSION` | the running build |
| `SHOW HELP` | the list above |

Both the simple and the extended query protocols are supported, so `psql` and a
driver such as pgx or the JDBC driver both work without special configuration.

Two constraints are deliberate:

- **The console requires `auth.mode: pontus`.** In passthrough mode a *backend*
  verifies the client's password, and the console has no backend to ask — so it
  refuses rather than admitting a client nothing authenticated.
- **`users` has no default and no wildcard.** An enabled console with nobody
  listed is refused at startup, because that configuration reads like
  "everyone".

`SHOW STATS` and `SHOW SERVERS` are not implemented: they report per-database
query and byte totals, and per-connection server detail, which Pontus does not
yet keep. They say so rather than returning zeros that would sit on a dashboard
looking like a working integration.

---

## Management API

The management API is built using **ConnectRPC**, which is compatible with gRPC. It handles everything from backend management to real-time telemetry.

- **Web Dashboard**: Access via `http://localhost:9090` (by default).
- **ConnectRPC/gRPC**: `http://localhost:9090/api.proto.ManagementService/`
- **SQL Clients**: Connect to `:5432` using any standard MySQL or PostgreSQL client (e.g., `psql`, `mysql` CLI).

## Development

### UI Development
To see the latest UI changes during development, you can use the built-in development proxy:

1. Start the Vite dev server:
   ```bash
   cd web; bun install; bun run dev
   ```

2. Run Pontus in a separate terminal:
   ```bash
   go run ./cmd/pontus -config config.yaml
   ```
   *Pontus will automatically detect the Vite dev server and proxy requests to it.*

---

## Examples

We provide example applications demonstrating how to connect to Pontus using Go's `database/sql` package.

### PostgreSQL Example
1. Ensure Pontus is running and configured for PostgreSQL (default).
2. Run the example:
   ```bash
   go run ./examples/postgres
   ```

### MySQL Example
1. Configure Pontus for MySQL in `config.yaml`:
   ```yaml
   protocol: "mysql"
   proxy_addr: ":3306"
   # ... update backends to point to MySQL servers
   ```
2. Run the example:
   ```bash
   go run ./examples/mysql
   ```

---

## License
MIT
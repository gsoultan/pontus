# Pontus

Pontus is a high-performance, cloud-native database connection pooler and load balancer written in Go 1.27. It is designed to scale database workloads by managing connection pools efficiently, providing advanced observability, and ensuring high availability.

## Features
- **Multi-Protocol Support**: PostgreSQL and MySQL/MariaDB with robust SQL parsing.
- **Transaction Mode Pooling**: Minimizes server connections by releasing them when idle.
- **Load Balancing**: Round Robin, Least Connections, and Consistent Hashing (Sticky).
- **Failover & Health Checks**: Passive and active health monitoring with Raft-driven consensus.
- **Security**: WAF 2.0 with regex patterns, global Rate Limiting, and Exfiltration Guard.
- **Intelligent Caching**: Semantic result-set cache with automatic table-level invalidation.
- **AI-Driven Insights**: Proactive query plan analysis (EXPLAIN) and optimization suggestions.
- **Advanced Observability**: Real-time Top Queries dashboard and OpenTelemetry tracing.
- **`pontusctl` CLI**: Dedicated CLI tool for management and status.
- **Web Dashboard**: Modern, built-in dashboard for monitoring (embedded in binary).
- **Adaptive Pooling**: BBR-style congestion control for DB connections with resource-aware throttling.
- **Performance Advisor**: Real-time suggestions for system tuning based on CPU, memory, and concurrency.
- **Low Footprint**: Zero-allocation buffer management and optimized connection handling (100k+ clients).

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

# Security (WAF)
firewall:
  enabled: true
  blocked_words: ["DROP", "TRUNCATE"]
  patterns: ["(?i)UNION\\s+SELECT"] # Custom regex patterns

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
```

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
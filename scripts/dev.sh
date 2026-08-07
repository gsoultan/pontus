#!/usr/bin/env bash
#
# dev.sh — run Pontus locally for development.
#
#   ./scripts/dev.sh              # full stack: postgres + agent + vite + pontus
#   ./scripts/dev.sh doctor       # report on the toolchain and ports, change nothing
#   ./scripts/dev.sh gen          # regenerate protobuf stubs + rebuild web/dist, then exit
#   ./scripts/dev.sh down         # stop the dev postgres container
#   ./scripts/dev.sh --reset      # wipe .dev/ (databases, config, binaries) and start fresh
#
# Flags:  --no-db  --no-ui  --no-agent  --rebuild-ui  --reset  -h|--help
# Env:    PROXY_PORT MGMT_PORT AGENT_PORT VITE_PORT
#         PG_HOST PG_PORT PG_USER PG_PASSWORD PG_DB
#
# Three things about this repo drive what happens below:
#   1. web/ui.go does //go:embed all:dist, so web/dist must exist before ANY go build
#      or go test that touches package web. It is gitignored, so a clean checkout has none.
#   2. pool.NewServer() rejects a backend with no agent_addr, so pontus-agent must be
#      running or every backend fails to construct and the proxy serves nothing.
#   3. /go.sum is gitignored and the on-disk copy is incomplete, so a clean checkout
#      fails to build until `go mod download all` fills it in.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# modernc.org/sqlite is the pure-Go driver; reintroducing cgo breaks every release target.
export CGO_ENABLED=0

DEV_DIR="$REPO_ROOT/.dev"
BIN_DIR="$DEV_DIR/bin"
DATA_DIR="$DEV_DIR/data"
LOG_DIR="$DEV_DIR/log"
CONFIG="$DEV_DIR/config.yaml"

# 6432, not 5432: Pontus is a pooler, and a dev box almost always already has a Postgres
# on 5432. This is the same convention pgbouncer uses. Override with PROXY_PORT=5432 if
# you specifically want clients to reach Pontus on the stock port.
PROXY_PORT="${PROXY_PORT:-6432}"
MGMT_PORT="${MGMT_PORT:-9090}"
AGENT_PORT="${AGENT_PORT:-9091}"
VITE_PORT="${VITE_PORT:-5173}"

PG_HOST="${PG_HOST:-127.0.0.1}"
PG_PORT="${PG_PORT:-5433}"      # not 5432 — Pontus itself listens there
PG_USER="${PG_USER:-postgres}"
PG_PASSWORD="${PG_PASSWORD:-postgres}"
PG_DB="${PG_DB:-postgres}"

PG_CONTAINER="pontus-dev-pg"
PG_IMAGE="${PG_IMAGE:-postgres:17-alpine}"

WITH_DB=1; WITH_UI=1; WITH_AGENT=1; REBUILD_UI=0; RESET=0
COMMAND="up"

CR=""          # container runtime binary, resolved in preflight
CR_KIND=""     # "docker" (docker/podman CLI dialect) or "apple" (macOS `container`)
MANAGED_DB=0   # 1 if we started the container ourselves
PIDS=()

# ---------------------------------------------------------------- output helpers

if [ -t 1 ]; then
  B=$'\033[1m'; DIM=$'\033[2m'; R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; C=$'\033[36m'; N=$'\033[0m'
else
  B=""; DIM=""; R=""; G=""; Y=""; C=""; N=""
fi

step() { printf '%s==>%s %s\n' "$C$B" "$N$B" "$*$N"; }
info() { printf '    %s\n' "$*"; }
ok()   { printf '    %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '    %s!%s %s\n' "$Y" "$N" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$R$B" "$N" "$*" >&2; exit 1; }

# Print the whole comment header, however long it grows.
usage() {
  awk 'NR > 1 { if ($0 !~ /^#/) exit; sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
  exit 0
}

# ---------------------------------------------------------------- arg parsing

while [ $# -gt 0 ]; do
  case "$1" in
    up|down|gen|doctor) COMMAND="$1" ;;
    --no-db)      WITH_DB=0 ;;
    --no-ui)      WITH_UI=0 ;;
    --no-agent)   WITH_AGENT=0 ;;
    --rebuild-ui) REBUILD_UI=1 ;;
    --reset)      RESET=1 ;;
    -h|--help)    usage ;;
    *) die "unknown argument: $1  (try --help)" ;;
  esac
  shift
done

# ---------------------------------------------------------------- utilities

have() { command -v "$1" >/dev/null 2>&1; }

# TCP probe via bash's /dev/tcp so we don't depend on nc being installed.
# The connect happens inside a subshell, so the descriptor is closed when it exits and the
# subshell's status is the answer. Do not touch fd 3 in the calling shell.
#
# /dev/tcp does not reliably reach IPv6-only listeners (bash resolves one family and stops),
# and Vite binds [::1] only, so loopback checks fall back to lsof when it is available.
port_open() {
  local host="$1" port="$2"
  (exec 3<>"/dev/tcp/$host/$port") >/dev/null 2>&1 && return 0
  case "$host" in
    127.0.0.1|localhost|::1)
      (exec 3<>"/dev/tcp/::1/$port") >/dev/null 2>&1 && return 0
      have lsof && lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 && return 0
      ;;
  esac
  return 1
}

port_busy() { port_open 127.0.0.1 "$1"; }

# curl gets the address family right on its own, so prefer it for anything that speaks HTTP.
http_ready() {
  have curl || return 1
  curl -fsS -o /dev/null --max-time 2 "$1" >/dev/null 2>&1
}

wait_for_port() {
  local host="$1" port="$2" label="$3" secs="${4:-45}" i=0
  while [ "$i" -lt $((secs * 2)) ]; do
    port_open "$host" "$port" && return 0
    sleep 0.5
    i=$((i + 1))
  done
  return 1
}

wait_for_http() {
  local url="$1" port="$2" secs="${3:-60}" i=0
  while [ "$i" -lt $((secs * 2)) ]; do
    http_ready "$url" && return 0
    # if curl is unavailable, settle for the port being open
    have curl || { port_open 127.0.0.1 "$port" && return 0; }
    sleep 0.5
    i=$((i + 1))
  done
  return 1
}

port_holder() {
  have lsof || return 0
  lsof -nP -iTCP:"$1" -sTCP:LISTEN -Fc 2>/dev/null | sed -n 's/^c//p' | head -1
}

# Pick the first free port at or above the requested one. A dev box usually already has
# something on 5432/9091, and making the user re-run with overrides for each collision is
# hostile. Writes the chosen port to stdout; notes go to stderr so $(...) stays clean.
resolve_port() {
  # Split: referring to $desired inside the same `local` that declares it trips set -u.
  local desired="$1" label="$2" var="$3"
  local p tries holder
  p="$desired"; tries=0; holder=""

  while port_busy "$p"; do
    tries=$((tries + 1))
    if [ "$tries" -gt 20 ]; then
      die "no free port in $desired-$p for the $label — set $var=<port> explicitly"
    fi
    p=$((p + 1))
  done

  if [ "$p" != "$desired" ]; then
    holder="$(port_holder "$desired")"
    [ -n "$holder" ] && holder=" (held by $holder)"
    warn "$label: $desired is taken$holder — using $p"
  fi
  printf '%s' "$p"
}

# Vite is the one port that cannot move: server/management/handler/ui.go hardcodes
# http://localhost:5173 in three places, so the dashboard proxy will not follow it.
require_vite_port() {
  [ "$WITH_UI" -eq 1 ] || return 0

  if [ "$VITE_PORT" != "5173" ]; then
    warn "ui.go hardcodes http://localhost:5173, so the dashboard proxy will not follow"
    warn "VITE_PORT=$VITE_PORT — expect a 502 on the dashboard"
  fi

  if port_busy "$VITE_PORT"; then
    die "port $VITE_PORT (Vite) is in use$( [ -n "$(port_holder "$VITE_PORT")" ] && printf ' by %s' "$(port_holder "$VITE_PORT")" ).
    ui.go hardcodes 5173 so it cannot be moved — free that port, or run with --no-ui."
  fi
}

track() { PIDS+=("$1"); }

cleanup() {
  local code=$?
  trap - EXIT INT TERM
  printf '\n'
  step "Shutting down"
  local pid
  for pid in "${PIDS[@]:-}"; do
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
      info "stopped pid $pid"
    fi
  done
  if [ "$MANAGED_DB" -eq 1 ] && [ -n "$CR" ]; then
    info "postgres container left running — ./scripts/dev.sh down to stop it"
  fi
  exit "$code"
}

# ---------------------------------------------------------------- preflight

preflight() {
  step "Preflight"

  have go  || die "go not found. Pontus needs Go 1.26+."
  ok "go        $(go version | awk '{print $3}')"

  have buf || die "buf not found. Install from https://buf.build/docs/installation — it generates both the Go stubs and web/src/gen."
  ok "buf       $(buf --version)"

  if have bun; then
    ok "bun       $(bun --version)"
  else
    die "bun not found. The dashboard build and dev server both need it (https://bun.sh)."
  fi

  detect_runtime
  if [ -n "$CR" ]; then
    ok "runtime   $CR"
  else
    info "runtime   ${DIM}no container runtime detected${N}"
  fi
}

# docker and podman share a CLI dialect. Apple's `container` (macOS 26) takes the same
# run/exec/start/stop flags but has no `info`, and its `ls` has no Go-template --format,
# so listing goes through --quiet (where the ID is the --name we gave it).
detect_runtime() {
  if have docker && docker info >/dev/null 2>&1; then
    CR="docker"; CR_KIND="docker"
  elif have podman && podman info >/dev/null 2>&1; then
    CR="podman"; CR_KIND="docker"
  elif have container && container system status >/dev/null 2>&1; then
    CR="container"; CR_KIND="apple"
  fi
}

cr_exists() {
  case "$CR_KIND" in
    docker) "$CR" ps -a --format '{{.Names}}' 2>/dev/null | grep -qx "$1" ;;
    apple)  "$CR" ls --all --quiet 2>/dev/null | grep -qx "$1" ;;
    *) return 1 ;;
  esac
}

cr_running() {
  case "$CR_KIND" in
    docker) "$CR" ps --format '{{.Names}}' 2>/dev/null | grep -qx "$1" ;;
    apple)  "$CR" ls --quiet 2>/dev/null | grep -qx "$1" ;;
    *) return 1 ;;
  esac
}

doctor() {
  preflight
  step "Ports"
  local p
  for p in "$PROXY_PORT:pontus proxy" "$MGMT_PORT:dashboard + ConnectRPC" \
           "$AGENT_PORT:pontus-agent" "$VITE_PORT:vite" "$PG_PORT:postgres backend"; do
    local port="${p%%:*}" label="${p#*:}"
    if port_busy "$port"; then
      warn "$port ${DIM}($label)${N} in use"
    else
      ok "$port ${DIM}($label)${N} free"
    fi
  done

  step "Repo state"
  if [ ! -d web/dist ]; then
    warn "web/dist missing — the Go build cannot compile package web until it exists"
  elif grep -q 'dashboard bundle not built' web/dist/index.html 2>/dev/null; then
    warn "web/dist is the placeholder, not a real bundle — run: bun run --cwd web build"
  else
    ok "web/dist present"
  fi
  [ -d web/node_modules ] && ok "web/node_modules present" || warn "web/node_modules missing"
  if go build ./pkg/config/ >/dev/null 2>&1; then
    ok "go.sum resolves"
  else
    warn "go.sum incomplete — run: go mod download all"
  fi
  [ -f "$CONFIG" ] && ok "dev config $CONFIG" || info "dev config will be generated at $CONFIG"
  exit 0
}

# ---------------------------------------------------------------- codegen & build

ensure_gosum() {
  if ! go build ./pkg/config/ >/dev/null 2>&1; then
    step "Completing go.sum"
    info "/go.sum is gitignored and incomplete on checkout; filling it in"
    go mod download all
    ok "dependencies resolved"
  fi
}

# buf needs four protoc plugins on PATH. Three are Go binaries that land in GOBIN; the
# fourth (protoc-gen-es, which emits web/src/gen) is a devDependency of web/, so it only
# exists after `bun install` and lives in web/node_modules/.bin.
proto_plugin_path() {
  local gobin
  gobin="$(go env GOBIN)"
  [ -n "$gobin" ] || gobin="$(go env GOPATH)/bin"
  export PATH="$gobin:$REPO_ROOT/web/node_modules/.bin:$PATH"
}

# Install a Go protoc plugin at the version this module already depends on, so the
# generated code matches the runtime libraries in go.mod.
install_go_plugin() {
  local bin="$1" pkg="$2" mod="$3" ver
  have "$bin" && return 0
  ver="$(go list -m -f '{{.Version}}' "$mod" 2>/dev/null || true)"
  [ -n "$ver" ] || ver="latest"
  info "installing $bin@$ver"
  go install "$pkg@$ver"
}

ensure_proto_plugins() {
  proto_plugin_path
  install_go_plugin protoc-gen-go         google.golang.org/protobuf/cmd/protoc-gen-go        google.golang.org/protobuf
  install_go_plugin protoc-gen-connect-go connectrpc.com/connect/cmd/protoc-gen-connect-go    connectrpc.com/connect
  install_go_plugin protoc-gen-go-grpc    google.golang.org/grpc/cmd/protoc-gen-go-grpc       google.golang.org/grpc

  if ! have protoc-gen-es; then
    die "protoc-gen-es not found. It is a devDependency of web/ — run: bun install --cwd web"
  fi
}

generate_proto() {
  step "Generating protobuf (Go stubs + web/src/gen)"
  ensure_proto_plugins
  buf generate
  ok "buf generate"
}

# The generated Go stubs and web/src/gen are committed, so a normal dev run never needs to
# regenerate. Run `./scripts/dev.sh gen` after editing a .proto. (No mtime heuristic here:
# a git checkout rewrites every timestamp, so it would warn on a clean tree every time.)

ensure_web_deps() {
  if [ ! -d web/node_modules ]; then
    step "Installing dashboard dependencies"
    bun install --cwd web
    ok "bun install"
  fi
}

# web/dist must exist for //go:embed all:dist to compile, even when Vite serves the real
# UI at runtime. If the bundle fails to build we still have to put *something* there or
# the Go build cannot compile package web at all — a broken dashboard should not block
# work on the proxy.
ensure_web_dist() {
  [ "$REBUILD_UI" -eq 1 ] || [ ! -d web/dist ] || return 0

  step "Building dashboard bundle"
  info "required by //go:embed all:dist — the Go build will not compile without it"

  if bun run --cwd web build; then
    ok "web/dist"
    return 0
  fi

  warn "dashboard build failed — falling back to a placeholder web/dist"
  warn "the Go build needs the directory to exist; the proxy and management API are unaffected"
  if [ "$WITH_UI" -eq 1 ]; then
    warn "Vite still serves the dashboard live, so only genuinely broken routes will fail"
  fi
  write_placeholder_dist
}

write_placeholder_dist() {
  mkdir -p web/dist
  cat > web/dist/index.html <<'EOF'
<!doctype html>
<meta charset="utf-8">
<title>Pontus — dashboard bundle not built</title>
<body style="font:14px system-ui;margin:3rem;max-width:44rem">
<h1>Dashboard bundle not built</h1>
<p>This is a placeholder written by <code>scripts/dev.sh</code> so that
<code>//go:embed all:dist</code> in <code>web/ui.go</code> can compile. The proxy and the
management API are running normally.</p>
<p>Rebuild with <code>bun run --cwd web build</code> to see the real error, or run the dev
server with <code>bun run --cwd web dev</code>.</p>
</body>
EOF
  ok "placeholder web/dist/index.html"
}

build_binaries() {
  step "Building binaries"
  mkdir -p "$BIN_DIR"
  go build -o "$BIN_DIR/pontus" ./cmd/pontus
  ok "pontus"
  if [ "$WITH_AGENT" -eq 1 ]; then
    go build -o "$BIN_DIR/pontus-agent" ./cmd/agent
    ok "pontus-agent"
  fi
}

# ---------------------------------------------------------------- postgres

start_db() {
  step "Postgres backend"

  if port_open "$PG_HOST" "$PG_PORT"; then
    ok "reachable at $PG_HOST:$PG_PORT (already running — not managed by this script)"
    return 0
  fi

  if [ -z "$CR" ]; then
    cat >&2 <<EOF
    ${R}No Postgres at $PG_HOST:$PG_PORT and no container runtime to start one.${N}

    Either start Docker/Podman and re-run, or point the script at an existing server:

        PG_HOST=<host> PG_PORT=<port> PG_USER=<user> PG_PASSWORD=<pw> ./scripts/dev.sh

    or skip database management entirely with --no-db (the proxy will start but every
    backend will be unhealthy until something is listening on $PG_HOST:$PG_PORT).
EOF
    die "no database available"
  fi

  if cr_exists "$PG_CONTAINER"; then
    info "starting existing container $PG_CONTAINER"
    "$CR" start "$PG_CONTAINER" >/dev/null
  else
    info "running $PG_IMAGE as $PG_CONTAINER on port $PG_PORT"
    # scram-sha-256 deliberately: it is PostgreSQL's default since 14 and the harder
    # path, so the dev loop exercises the real handshake rather than a trust shortcut.
    "$CR" run -d --name "$PG_CONTAINER" \
      -e POSTGRES_USER="$PG_USER" \
      -e POSTGRES_PASSWORD="$PG_PASSWORD" \
      -e POSTGRES_DB="$PG_DB" \
      -e POSTGRES_HOST_AUTH_METHOD=scram-sha-256 \
      -e POSTGRES_INITDB_ARGS="--auth-host=scram-sha-256" \
      -p "$PG_PORT:5432" \
      "$PG_IMAGE" >/dev/null
  fi
  MANAGED_DB=1

  wait_for_port "$PG_HOST" "$PG_PORT" "postgres" 60 \
    || die "postgres did not open $PG_PORT in time (check: $CR logs $PG_CONTAINER)"

  # An open port is not the same as ready to accept queries.
  local i=0
  while [ "$i" -lt 60 ]; do
    if "$CR" exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -q >/dev/null 2>&1; then
      ok "postgres ready on $PG_HOST:$PG_PORT"
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done
  warn "pg_isready never succeeded; continuing anyway"
}

stop_db() {
  preflight
  step "Stopping dev postgres"
  [ -n "$CR" ] || die "no container runtime detected"
  if cr_running "$PG_CONTAINER"; then
    "$CR" stop "$PG_CONTAINER" >/dev/null
    ok "stopped $PG_CONTAINER"
  else
    info "$PG_CONTAINER is not running"
  fi
  exit 0
}

# ---------------------------------------------------------------- config

# The config file is what Pontus actually binds, so it has to agree with the ports this
# run advertises. Reconcile before resolving ports, not after.
cfg_port() {
  sed -n "s/^$1: *\"\{0,1\}:\([0-9]\{1,\}\)\"\{0,1\}.*/\1/p" "$CONFIG" 2>/dev/null | head -1
}

reconcile_config() {
  [ -f "$CONFIG" ] || return 0

  local cp mp
  cp="$(cfg_port proxy_addr)"
  mp="$(cfg_port mgmt_addr)"
  [ -n "$cp" ] || return 0

  if [ "$cp" = "$PROXY_PORT" ] && [ "$mp" = "$MGMT_PORT" ]; then
    return 0
  fi

  if grep -q 'Generated by scripts/dev.sh' "$CONFIG" 2>/dev/null; then
    step "Refreshing dev config"
    info "it binds :$cp/:$mp but this run wants :$PROXY_PORT/:$MGMT_PORT — regenerating"
    rm -f "$CONFIG"
    # config.yaml is read ONCE: internal/app.bootstrapFromConfig() returns early as soon as
    # the project store has any row, after which management.db owns proxy_addr and the
    # backend list. Leaving the old store here means Pontus binds the previous port and
    # keeps dialling the previous backend, whatever the new config says.
    if [ -d "$DATA_DIR" ]; then
      rm -rf "$DATA_DIR"
      info "cleared $DATA_DIR so the new config is re-migrated into the store"
    fi
  else
    warn "$CONFIG is hand-edited and binds :$cp (proxy) / :$mp (dashboard) — honouring it"
    PROXY_PORT="$cp"
    MGMT_PORT="$mp"
  fi
}

write_config() {
  [ -f "$CONFIG" ] && { info "using existing $CONFIG"; return 0; }

  step "Generating dev config"
  mkdir -p "$DEV_DIR" "$DATA_DIR"

  # Real secrets even in dev: an empty jwt_secret silently falls back to the literal
  # "pontus-secret-key", and an empty admin_token makes the auth interceptor a no-op.
  local jwt admin
  jwt="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  admin="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"

  cat > "$CONFIG" <<EOF
# Generated by scripts/dev.sh — safe to edit, regenerate by deleting this file.
proxy_addr: ":$PROXY_PORT"
mgmt_addr: ":$MGMT_PORT"
protocol: postgres
pooling_mode: transaction
balancer: p2c
data_dir: "$DATA_DIR"

dial_timeout: 5s
health_interval: 10s
# Note: the query-timeout context is created before the client read, so a session idle
# longer than this gets an already-expired context on its next query. Keep it generous.
query_timeout: 60s
max_conns: 50
min_idle: 2

backends:
  # agent_addr is mandatory — pool.NewServer() returns an error without it and the
  # backend is never created.
  - addr: "$PG_HOST:$PG_PORT"
    agent_addr: "127.0.0.1:$AGENT_PORT"
    role: primary
    weight: 1
    zone: local

# Writes now invalidate the tables they touch, and keys are namespaced by backend,
# database, user and session state, so this is safe to leave on in dev.
cache:
  enabled: true
  ttl: 30s
  max_size: 1000

# Disabled by default: blocked words still match with strings.Contains, so a column
# named "dropdown" trips a DROP rule (mem:findings C16), and the WAF still inspects the
# normalized text rather than the bytes that reach the backend (B1).
firewall:
  enabled: false
  blocked_words: []
  max_response_size_mb: 64

rate_limit:
  enabled: false
  rps: 500
  burst: 1000

# No client-facing TLS: the listener is a plain net.Listen and there is no Postgres
# SSLRequest handling, so clients must connect with sslmode=disable.
jwt_secret: "$jwt"
admin_token: "$admin"
EOF

  ok "wrote $CONFIG"
  info "${DIM}jwt_secret and admin_token were generated randomly${N}"
}

# ---------------------------------------------------------------- processes

start_agent() {
  [ "$WITH_AGENT" -eq 1 ] || { warn "agent disabled — every backend will fail to construct (agent_addr is mandatory)"; return 0; }

  step "Starting pontus-agent"
  mkdir -p "$LOG_DIR"
  "$BIN_DIR/pontus-agent" -addr ":$AGENT_PORT" >"$LOG_DIR/agent.log" 2>&1 &
  track $!
  if wait_for_port 127.0.0.1 "$AGENT_PORT" "agent" 20; then
    ok "agent on :$AGENT_PORT  ${DIM}($LOG_DIR/agent.log)${N}"
  else
    die "agent failed to start — see $LOG_DIR/agent.log"
  fi
}

start_vite() {
  [ "$WITH_UI" -eq 1 ] || return 0

  step "Starting Vite dev server"
  mkdir -p "$LOG_DIR"
  bun run --cwd web dev >"$LOG_DIR/vite.log" 2>&1 &
  track $!
  # Vite binds [::1] only, so probe over HTTP by name rather than by IPv4 address.
  if wait_for_http "http://localhost:$VITE_PORT" "$VITE_PORT" 60; then
    ok "vite on :$VITE_PORT  ${DIM}($LOG_DIR/vite.log)${N}"
  else
    die "vite failed to start — see $LOG_DIR/vite.log"
  fi
}

banner() {
  local admin_token
  admin_token="$(grep '^admin_token:' "$CONFIG" | cut -d'"' -f2)"

  printf '\n%s' "$B"
  cat <<EOF
  Pontus is up
$N
  ${B}Postgres (via Pontus)${N}  postgresql://$PG_USER:$PG_PASSWORD@127.0.0.1:$PROXY_PORT/$PG_DB?sslmode=disable
  ${B}Postgres (direct)${N}      postgresql://$PG_USER:$PG_PASSWORD@$PG_HOST:$PG_PORT/$PG_DB?sslmode=disable
  ${B}Dashboard${N}              http://localhost:$MGMT_PORT
  ${B}ConnectRPC${N}             http://localhost:$MGMT_PORT/api.proto.service.ManagementService/
  ${B}Metrics${N}                http://localhost:$MGMT_PORT/metrics
  ${B}Login${N}                  admin / admin123   ${DIM}(auto-created on first run)${N}
  ${B}admin_token${N}            $admin_token

  ${Y}sslmode=disable is required.${N} There is no SSLRequest handling on the wire, so a
  client negotiating TLS (libpq's default sslmode=prefer) will hang instead of connecting.

EOF
  if [ "$WITH_UI" -eq 1 ]; then
    printf '  %sUI hot reload is on%s — PONTUS_DEV=true proxies :%s to Vite on :%s\n\n' "$DIM" "$N" "$MGMT_PORT" "$VITE_PORT"
  fi
  printf '  %sCtrl-C to stop.%s\n\n' "$DIM" "$N"
}

run_pontus() {
  step "Starting Pontus"

  # PONTUS_DEV=true does two things: it skips EnsureUIBuilt() (which otherwise shells out
  # to `bun install && bun run build` on every single startup), and it makes the mgmt
  # server proxy the dashboard to Vite. It proxies unconditionally, so only set it when
  # Vite is actually running — otherwise the dashboard 502s.
  if [ "$WITH_UI" -eq 1 ]; then
    export PONTUS_DEV=true
  else
    unset PONTUS_DEV || true
    warn "serving the static web/dist build; startup will re-run bun install + bun run build"
  fi

  # Deliberately not exec: this shell has to stay alive to run the cleanup trap, or the
  # agent and Vite are orphaned when Pontus exits.
  "$BIN_DIR/pontus" -config "$CONFIG" &
  local pontus_pid=$!
  track "$pontus_pid"

  if wait_for_port 127.0.0.1 "$MGMT_PORT" "management" 30; then
    ok "management API on :$MGMT_PORT"
    banner
  else
    warn "management API never opened :$MGMT_PORT — see the log above"
  fi

  wait "$pontus_pid"
}

# ---------------------------------------------------------------- main

case "$COMMAND" in
  doctor) doctor ;;
  down)   stop_db ;;
esac

trap cleanup EXIT INT TERM

preflight

# After preflight so $CR is resolved — otherwise this hardcodes a runtime that may not exist.
if [ "$RESET" -eq 1 ]; then
  step "Resetting dev state"
  if [ -n "$CR" ]; then
    "$CR" rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
    info "removed container $PG_CONTAINER"
  fi
  rm -rf "$DEV_DIR"
  ok "removed $DEV_DIR"
fi

ensure_gosum
ensure_web_deps          # must precede generation: protoc-gen-es comes from web/node_modules

if [ "$COMMAND" = "gen" ]; then
  generate_proto
  REBUILD_UI=1
  ensure_web_dist
  trap - EXIT INT TERM
  step "Done"
  ok "protobuf stubs and dashboard bundle regenerated"
  exit 0
fi


ensure_web_dist

# An existing config wins over the defaults, so settle the ports before probing them.
reconcile_config

PROXY_PORT="$(resolve_port "$PROXY_PORT" "proxy listener"  PROXY_PORT)"
MGMT_PORT="$(resolve_port "$MGMT_PORT"  "dashboard + API" MGMT_PORT)"
if [ "$WITH_AGENT" -eq 1 ]; then
  AGENT_PORT="$(resolve_port "$AGENT_PORT" "agent" AGENT_PORT)"
fi
require_vite_port

# resolve_port may have moved something after reconcile_config approved the file.
reconcile_config

build_binaries
[ "$WITH_DB" -eq 1 ] && start_db
write_config
start_agent
start_vite
run_pontus

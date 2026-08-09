#!/usr/bin/env bash
#
# Stand up a primary + streaming replica for the two-backend E2E tests.
#
#   ./scripts/e2e-cluster.sh up       # create and start both, print the exports
#   ./scripts/e2e-cluster.sh down     # remove both
#   ./scripts/e2e-cluster.sh status   # roles, receiver, lag
#
# A dedicated pair, deliberately: the tests need to stop the primary and promote
# the replica, and doing that to whatever database happens to be on 5432 would
# take a dev box down with it. Nothing here touches an existing container.
#
# Replication is routed through the *host gateway* to the primary's published
# port rather than to its container IP. Container IPs change when a container
# restarts — the first cut of this pinned primary_conninfo to the IP, and the
# replica silently never reconnected after a restart, which is precisely the
# state one of the tests has to be able to leave and recover from.
set -euo pipefail

PRIMARY_NAME="${PRIMARY_NAME:-pontus-e2e-primary}"
REPLICA_NAME="${REPLICA_NAME:-pontus-e2e-replica}"
# 55432/55433 was the obvious pick and collided with another project's dev
# database on the same machine. The collision did not look like a collision: the
# other container answered on 127.0.0.1 and rejected our credentials, so Pontus
# reported "password authentication failed" and looked like an auth bug. Hence
# both the less obvious defaults and the preflight below.
PRIMARY_PORT="${PRIMARY_PORT:-55832}"
REPLICA_PORT="${REPLICA_PORT:-55833}"
PG_IMAGE="${PG_IMAGE:-postgres:17-alpine}"
PG_USER="${PG_USER:-postgres}"
PG_PASSWORD="${PG_PASSWORD:-postgres}"
PG_DB="${PG_DB:-postgres}"

B=$'\033[1m'; R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; DIM=$'\033[2m'; N=$'\033[0m'
step() { printf '\n%s==>%s %s%s%s\n' "$B" "$N" "$B" "$1" "$N"; }
info() { printf '    %s\n' "$1"; }
ok()   { printf '    %s✓%s %s\n' "$G" "$N" "$1"; }
warn() { printf '    %s!%s %s\n' "$Y" "$N" "$1"; }
die()  { printf '\n%serror:%s %s\n\n' "$R" "$N" "$1" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

CR=""; CR_KIND=""

detect_runtime() {
  if have docker && docker info >/dev/null 2>&1; then
    CR="docker"; CR_KIND="docker"
  elif have podman && podman info >/dev/null 2>&1; then
    CR="podman"; CR_KIND="docker"
  elif have container && container system status >/dev/null 2>&1; then
    CR="container"; CR_KIND="apple"
  fi
  [ -n "$CR" ] || die "no container runtime detected (docker, podman, or Apple container)"
}

cr_exists() {
  case "$CR_KIND" in
    docker) "$CR" ps -a --format '{{.Names}}' 2>/dev/null | grep -qx "$1" ;;
    apple)  "$CR" ls --all --quiet 2>/dev/null | grep -qx "$1" ;;
  esac
}

cr_running() {
  case "$CR_KIND" in
    docker) "$CR" ps --format '{{.Names}}' 2>/dev/null | grep -qx "$1" ;;
    apple)  "$CR" ls --quiet 2>/dev/null | grep -qx "$1" ;;
  esac
}

wait_ready() {
  local name="$1" label="$2" i=0
  while [ "$i" -lt 90 ]; do
    if "$CR" exec "$name" pg_isready -U "$PG_USER" -q >/dev/null 2>&1; then
      ok "$label ready"
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done
  "$CR" logs "$name" 2>&1 | tail -20
  die "$label never became ready"
}

start_primary() {
  step "Primary on :$PRIMARY_PORT"
  if cr_running "$PRIMARY_NAME"; then
    ok "already running"
  else
    if cr_exists "$PRIMARY_NAME"; then
      "$CR" start "$PRIMARY_NAME" >/dev/null
      info "started existing $PRIMARY_NAME"
    else
      "$CR" run -d --name "$PRIMARY_NAME" \
        -e POSTGRES_USER="$PG_USER" \
        -e POSTGRES_PASSWORD="$PG_PASSWORD" \
        -e POSTGRES_DB="$PG_DB" \
        -e POSTGRES_HOST_AUTH_METHOD=scram-sha-256 \
        -e POSTGRES_INITDB_ARGS="--auth-host=scram-sha-256" \
        -p "$PRIMARY_PORT:5432" \
        "$PG_IMAGE" >/dev/null
      info "created $PRIMARY_NAME from $PG_IMAGE"
    fi
  fi
  wait_ready "$PRIMARY_NAME" "primary"

  # pg_hba's `all` in the database column does not cover replication connections
  # — that needs its own line, and the stock file only allows localhost. Adding
  # it is idempotent, so re-running `up` is safe.
  if ! "$CR" exec "$PRIMARY_NAME" grep -q '^host  *replication  *all  *all' \
      /var/lib/postgresql/data/pg_hba.conf 2>/dev/null; then
    "$CR" exec "$PRIMARY_NAME" sh -c \
      "echo 'host replication all all scram-sha-256' >> /var/lib/postgresql/data/pg_hba.conf"
    "$CR" exec "$PRIMARY_NAME" psql -U "$PG_USER" -qtAc "SELECT pg_reload_conf()" >/dev/null
    ok "replication allowed from any host"
  else
    ok "replication rule already present"
  fi
}

start_replica() {
  step "Replica on :$REPLICA_PORT"
  if cr_running "$REPLICA_NAME"; then
    ok "already running"
    wait_ready "$REPLICA_NAME" "replica"
    return
  fi

  # Always rebuilt rather than restarted. A standby that has been stopped may be
  # arbitrarily far behind, and a test that starts against a cold replica is
  # measuring the catch-up, not the thing it meant to.
  "$CR" rm -f "$REPLICA_NAME" >/dev/null 2>&1 || true

  "$CR" run -d --name "$REPLICA_NAME" \
    -e PGPASSWORD="$PG_PASSWORD" \
    -p "$REPLICA_PORT:5432" \
    "$PG_IMAGE" \
    bash -c "set -e
      # The default route's gateway is the host, on both Docker and Apple's
      # runtime. Going via the host's published port keeps primary_conninfo
      # valid across a primary restart, which changes its container IP.
      GW=\$(ip route | awk '/^default/{print \$3}')
      echo \"streaming from \$GW:$PRIMARY_PORT\"
      rm -rf /tmp/standby && mkdir -p /tmp/standby
      chown postgres:postgres /tmp/standby && chmod 700 /tmp/standby
      gosu postgres pg_basebackup -h \"\$GW\" -p $PRIMARY_PORT -U $PG_USER \
        -D /tmp/standby -Fp -Xs -R -w
      exec gosu postgres postgres -D /tmp/standby -c listen_addresses=0.0.0.0" >/dev/null

  wait_ready "$REPLICA_NAME" "replica"

  local i=0
  while [ "$i" -lt 60 ]; do
    if [ "$("$CR" exec "$REPLICA_NAME" psql -U "$PG_USER" -tAc \
        'SELECT pg_is_in_recovery()' 2>/dev/null | tr -d '[:space:]')" = "t" ]; then
      ok "in recovery"
      break
    fi
    sleep 1
    i=$((i + 1))
  done

  i=0
  while [ "$i" -lt 60 ]; do
    if [ "$("$CR" exec "$REPLICA_NAME" psql -U "$PG_USER" -tAc \
        'SELECT count(*) FROM pg_stat_wal_receiver' 2>/dev/null | tr -d '[:space:]')" = "1" ]; then
      ok "WAL receiver attached — streaming"
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done
  "$CR" logs "$REPLICA_NAME" 2>&1 | tail -20
  die "replica never attached a WAL receiver"
}

# port_free fails if anything is already listening, whoever owns it.
#
# A more specific bind (127.0.0.1:PORT) wins over a container publishing on
# *:PORT, so a foreign listener silently intercepts every connection the tests
# make. Checking up front turns a confusing authentication error into a sentence
# naming the port.
port_free() {
  local port="$1"
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    return 1
  fi
  return 0
}

preflight_ports() {
  local port label
  for pair in "$PRIMARY_PORT:primary" "$REPLICA_PORT:replica"; do
    port="${pair%%:*}"; label="${pair##*:}"
    if cr_running "$PRIMARY_NAME" && [ "$label" = "primary" ]; then
      continue # our own container already holds it
    fi
    if cr_running "$REPLICA_NAME" && [ "$label" = "replica" ]; then
      continue
    fi
    if ! port_free "$port"; then
      warn "port $port ($label) is already in use by another process"
      lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | tail -n +2 | head -3 | sed 's/^/      /'
      die "choose different ports: PRIMARY_PORT=... REPLICA_PORT=... $0 up
    A foreign listener on these ports does not fail loudly — it answers and
    rejects our credentials, which reads as an authentication bug in Pontus."
    fi
  done
}

cluster_up() {
  detect_runtime
  preflight_ports
  start_primary
  start_replica

  step "Ready"
  cat <<EOF
    Export these, then run the suite:

      export PONTUS_E2E_BACKEND=127.0.0.1:$PRIMARY_PORT
      export PONTUS_E2E_REPLICA=127.0.0.1:$REPLICA_PORT
      go test -tags=e2e -race ./e2e/ -run TestReplica

    Tear down with: ./scripts/e2e-cluster.sh down
EOF
}

cluster_down() {
  detect_runtime
  step "Removing the E2E cluster"
  local name
  for name in "$REPLICA_NAME" "$PRIMARY_NAME"; do
    if cr_exists "$name"; then
      "$CR" rm -f "$name" >/dev/null 2>&1 || true
      ok "removed $name"
    else
      info "$name not present"
    fi
  done
}

cluster_status() {
  detect_runtime
  step "Cluster"
  local name label
  for pair in "$PRIMARY_NAME:primary" "$REPLICA_NAME:replica"; do
    name="${pair%%:*}"; label="${pair##*:}"
    if ! cr_running "$name"; then
      warn "$label ($name) not running"
      continue
    fi
    local row
    row=$("$CR" exec "$name" psql -U "$PG_USER" -tAc \
      "SELECT CASE WHEN pg_is_in_recovery() THEN 'replica' ELSE 'primary' END
            ||' receivers='||(SELECT count(*) FROM pg_stat_wal_receiver)
            ||' replay_age='||round(COALESCE(EXTRACT(EPOCH FROM (now()-pg_last_xact_replay_timestamp())),0)::numeric,1)||'s'" \
      2>/dev/null | tr -d '\r')
    ok "$label ($name): ${row:-unreachable}"
  done
}

case "${1:-up}" in
  up)     cluster_up ;;
  down)   cluster_down ;;
  status) cluster_status ;;
  *) die "usage: $0 [up|down|status]" ;;
esac

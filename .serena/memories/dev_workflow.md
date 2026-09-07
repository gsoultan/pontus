# Running Pontus for Development

`scripts/dev.sh` orchestrates the whole local stack. It exists because four things about
this repo make a naive `go run ./cmd/pontus` fail, and none of them are guessable.

```bash
./scripts/dev.sh            # postgres + agent + vite + pontus, Ctrl-C tears it all down
./scripts/dev.sh doctor     # toolchain, ports and repo state; changes nothing
./scripts/dev.sh gen        # regenerate protobuf stubs + rebuild web/dist
./scripts/dev.sh down       # stop the dev postgres container
./scripts/dev.sh --reset    # wipe .dev/ and start clean
```

Flags: `--no-db --no-ui --no-agent --rebuild-ui --reset`.
Env: `PROXY_PORT MGMT_PORT AGENT_PORT VITE_PORT PG_HOST PG_PORT PG_USER PG_PASSWORD PG_DB`.
Everything it generates lives in `.dev/` (gitignored): config, SQLite data, logs, binaries.

## The four traps it works around

1. **`web/dist` must exist before the Go build, not after.** `web/ui.go` does
   `//go:embed all:dist` and `web/dist` is gitignored, so a clean checkout cannot compile
   package `web` — including `go test ./...`. The script builds it, and falls back to a
   placeholder `index.html` if the bundle fails so backend work is not blocked.
2. **`agent_addr` is mandatory.** `pool.NewServer` returns an error without it, so every
   backend silently fails to construct unless `pontus-agent` is running. The script starts
   it on `:9091` and points the generated config at it.
3. **`go.sum` is gitignored and incomplete** — `go build ./...` fails until
   `go mod download all` fills it in. The script does that when it detects the failure.
4. **`sslmode=disable` is mandatory.** There is no `SSLRequest` handling on the wire, so a
   client negotiating TLS (libpq's default `sslmode=prefer`) hangs instead of connecting.

## What actually happens when you connect

The stack comes up cleanly, but the proxy itself is broken on the wire (`mem:findings` W1–W3):
the first client connection runs one query, its second query fails with
`prepared statement … already exists`, and every later connection fails with
`invalid frontend message type 0`. The dev DB runs `POSTGRES_HOST_AUTH_METHOD=trust` because
`scram-sha-256` cannot complete a handshake through Pontus at all. Expect that — it is not
a problem with the script or your setup.

## Facts worth keeping

- **ConnectRPC prefix is `/api.proto.service.ManagementService/`** — note the `service`
  segment. A wrong prefix falls through the mux to the dashboard handler, so you get the
  SPA or a 502 from the Vite proxy, never a 404.
- **`PONTUS_DEV=true` proxies the dashboard to Vite unconditionally** and skips
  `EnsureUIBuilt()`. Only set it when Vite is actually running or `:9090` 502s. Leaving it
  unset makes the server shell out to `bun install && bun run build` on every startup.
- **Vite binds `[::1]` only.** Bash's `/dev/tcp` does not reach IPv6-only listeners, so
  readiness checks against `127.0.0.1:5173` never succeed — probe with `curl http://localhost`.
- **Generate the config with real secrets.** An empty `jwt_secret` falls back to the literal
  `"pontus-secret-key"` and an empty `admin_token` makes the auth interceptor a no-op, so a
  dev run with defaults is wide open and does not exercise the auth path at all. With both
  set, `GetStatus` correctly returns 401 without a token — verified.
- The dev config disables `cache` by default: the cache is never invalidated
  on writes and the blocked-word match is `strings.Contains`. Enable them deliberately.
  See `mem:findings` A1 and C16.
- macOS has no `setsid` and no `timeout`; don't reach for them in scripts here.

## Running the e2e suite

Behind the `e2e` build tag and it needs a real PostgreSQL. `requireBackend`
**skips** rather than fails without one, so a green run proves nothing until you
check it actually ran.

The variable is `PONTUS_E2E_BACKEND` (not `..._ADDR`; the default is
`127.0.0.1:5433`). A backend on the default port that Pontus cannot log into
produces confusing failures deep in SCRAM rather than a skip.

```bash
podman run -d --name pontus-e2e-pg -e POSTGRES_PASSWORD=pontus_e2e \
  -p 55432:5432 docker.io/library/postgres:16

PONTUS_E2E_BACKEND=127.0.0.1:55432 PONTUS_E2E_USER=postgres \
PONTUS_E2E_PASSWORD=pontus_e2e PONTUS_E2E_DB=postgres \
  go test -tags e2e ./e2e/ -run <Name> -v -timeout 15m
```

Each test builds the binary and starts a whole stack, so a single test is ~5 s
and the suite is minutes. `auth.mode: pontus` additionally needs a backend
`admin_dsn` or an `auth_file` — without either, `buildCredentialStore` logs the
reason and **silently stays in passthrough**, which reads as a feature not
working rather than as a misconfiguration. The harness template already sets
`admin_dsn`.


## Regenerating protobuf without reddening CI

CI installs **exact** protoc plugin versions and fails on any `buf generate`
diff. Generating with whatever is on your PATH rewrites the generator header in
every `.pb.go` and fails the build on nine files you did not touch — the error
reads "buf generate produced changes that were not committed" and names them
all, which looks like a much bigger problem than it is.

Install the pinned set into a temporary GOBIN so a newer toolchain elsewhere is
left alone (the recipe is also in `AGENTS.md`):

```bash
export GOBIN=$(mktemp -d)
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.19.2
export PATH="$GOBIN:$PWD/web/node_modules/.bin:$PATH"   # protoc-gen-es lives in web/
buf generate
```

The pinned versions are in `.github/workflows/ci.yml`; check there rather than
trusting the list above.

## Reproducing the lint gate

CI runs golangci-lint with `only-new-issues: true`, so the tree's few hundred
pre-existing findings do not count and yours do. Reproduce it exactly with:

```bash
golangci-lint run --new-from-merge-base=main ./...
```

Plain `golangci-lint run` reports everything and tells you nothing about whether
CI will pass.

## The e2e cluster step flakes

`./scripts/e2e-cluster.sh up` sometimes fails in CI with `psql: ... .s.PGSQL.5432
failed: No such file or directory` — a startup race, not a branch problem. Re-run
the job before hunting for a cause.

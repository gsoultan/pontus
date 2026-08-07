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
- The dev config disables `cache` and `firewall` by default: the cache is never invalidated
  on writes and the blocked-word match is `strings.Contains`. Enable them deliberately.
  See `mem:findings` A1 and C16.
- macOS has no `setsid` and no `timeout`; don't reach for them in scripts here.

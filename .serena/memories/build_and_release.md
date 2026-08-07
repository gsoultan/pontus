# Pontus Build & Release

## The non-obvious part: generation is an *input* to the Go build

`web/ui.go` does `//go:embed all:dist`, and `web/dist/` is gitignored. So **`web/dist` must
exist before any `go build` or `go test` that touches package `web`** — which includes
`go test ./...`. The same is true of the protobuf stubs.

```bash
buf generate                                          # Go stubs + web/src/gen (TS)
bun install --cwd web && bun run --cwd web build      # creates web/dist
# both of the above, plus the install, are what `go generate ./cmd/pontus` runs
```

`cmd/pontus/main.go` carries the directives:
```go
//go:generate buf generate
//go:generate bun --cwd ../../web install
//go:generate bun --cwd ../../web run build
```

## Verify

```bash
gofmt -l . && go vet ./... && go test -race ./...
bun run --cwd web lint
go build -o pontus ./cmd/pontus
go build -o pontus-agent ./cmd/agent
go build -o pontusctl ./cmd/pontusctl
```

## Run

```bash
./pontus -config config.yaml          # proxy :5432, dashboard + ConnectRPC :9090
./pontus -service install -config <abs path>   # kardianos service; also stop/uninstall/status
./pontus create-user -username u -password p -role admin -db management.db
./pontus-agent -addr :9091
```

Config is YAML (`pkg/config/options.go` and siblings): `proxy_addr`, `mgmt_addr`, `protocol`,
`pooling_mode`, `balancer`, `backends[]` (addr, agent_addr, role, weight, zone),
`firewall`, `cache`, `rate_limit`, `tls`, `backend_tls`, `shadow_backends`, `jwt_secret`,
`admin_token`, `data_dir`, `query_timeout`, `max_conns`, `min_idle`, `dial_timeout`.

## Load-bearing constraints

- **`CGO_ENABLED=0`.** `modernc.org/sqlite` is the pure-Go driver. Swapping in
  `mattn/go-sqlite3` (or any cgo dep) breaks every GoReleaser target.
- Generated code changes **only** via `buf generate`. Never hand-edit `*.pb.go`,
  `*connect*.go`, or `web/src/gen/`. CI fails the build if `buf generate` produces a diff.
- Go 1.26.1. The codebase uses 1.26 idioms deliberately: `new(val)` with a composite
  literal, `wg.Go(fn)`, range-over-func iterators (`protocol.Tokenize`, `slices.Values`),
  `omitzero` JSON tags.

## CI — `.github/workflows/ci.yml`

Go 1.26 + Bun + Buf → `go mod download` + `bun install` → `buf generate` → uncommitted-diff
check → golangci-lint → `go test -v ./...` → web lint → web build → `go build` both
binaries → `goreleaser check`.

Two ordering/consistency problems live here; see `mem:findings` items 1 and 2.

## Release — `.goreleaser.yaml`

Tag `v*` triggers it. Pre-hooks: `go mod tidy`, `buf generate`, `bun install`, `bun run build`.
Two builds (`pontus`, `pontus-agent`) × linux/windows/darwin × amd64/arm64, `CGO_ENABLED=0`,
ldflags stamping `pkg/version.{Version,Commit,BuildTime}`.

No `.golangci.yml` exists, so CI lints with the default linter set only.

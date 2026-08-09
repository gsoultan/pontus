# Pontus Core Memory

Root of the Pontus knowledge graph. Read this first, then follow only the `mem:`
references this task needs. Do not re-derive from source what is already written here.

## Process (read first, applies to every task)
- The mandatory Discover → Work → Verify → Persist loop, the Graphify/Serena/Obsidian/
  skills.sh entry points and the token-reduction ladder: `mem:always_optimize`.
- The Driver/Challenger profile panel and what "done" means: `mem:working_agreements`.

## What Pontus is
- One-paragraph identity, the two planes, and the request lifecycle: `mem:architecture`.
- Data plane internals — proxy, pool, protocol, balancer, cache, HA: `mem:data_plane`.
- The pooling engine is gpool's `pkg/pooling`, not ours: `mem:pool_engine`.
- Control plane internals — ConnectRPC management, agent, dashboard, stores: `mem:control_plane`.

## Build & ship
- The non-obvious generate-before-build order, CGO constraint, CI and release:
  `mem:build_and_release`.
- Dashboard toolchain pins and why the linter is oxlint rather than ESLint
  (TypeScript 7 hard-breaks typescript-eslint): `mem:web_toolchain`.
- Why the stores are SQLite and stay SQLite — Postgres and Pebble both
  benchmarked and rejected, with the numbers: `mem:storage_engine`.
- Running the stack locally — `scripts/dev.sh`, and the four traps that make a naive
  `go run ./cmd/pontus` fail: `mem:dev_workflow`.

## Security & correctness
- Trust boundaries, WAF, auth invariants and what must never regress: `mem:security`.
- What the agent orchestrates, what failover actually does, and why failback does **not**
  exist despite a comment that said it did: `mem:failover`.
- How pgpool-II sequences failover and what its `auto_failback` actually means (re-attaching a
  standby, never returning the write role), plus the gap table against Pontus:
  `mem:pgpool_failover_model`.
- The design for Pontus-side backend authentication — the one capability that closes A8, A9,
  W2 and W4 together, including why a stored SCRAM verifier is not enough and how ClientKey
  is recovered from the client's own proof: `docs/design/backend-auth.md`.
- Open findings, highest severity first: `mem:findings`. **Read the "Dead code that silently
  disables a whole subsystem" section before reasoning about the cache, the balancer or TLS** —
  cache invalidation, the load-balancer cost function and client-facing TLS all look implemented
  and are not wired to anything. Items marked `[repro]` were demonstrated, not inferred.

## Authoritative files in-repo
- `CLAUDE.md` — quick reference + the Always-Optimize Loop.
- `AGENTS.md` — the profile roster with per-surface Owns · Vetoes · Proof, and the
  definition of done.
- `.junie/guidelines.md` — Go and React code style (SOLID, one struct/interface per file,
  no stuttering, Go 1.26 idioms, functional React).

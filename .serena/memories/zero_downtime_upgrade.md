# Zero-downtime binary upgrade

Added 2026-09-11. Replacing the binary used to be an outage: the old process holds
the listening port until it exits, so the new one cannot bind.

## How it works

`reuse_port: true` (off by default, unix only) puts `SO_REUSEPORT` on every
listener, so both processes bind the same address and the new one serves before
the old one stops.

```bash
pontus -config /etc/pontus/config.yaml &   # binds alongside, serves immediately
kill -TERM "$OLD_PID"                      # drains in-flight statements, exits
```

`pkg/listen` owns both halves — the listener (`Config.TCP`) and the orchestration
lock. Wired at `internal/app` (management listener) and
`server/management/infrastructure/registry` (proxy listeners + the lock claim).

## The hazard is orchestration, not the listener

Sharing a port is safe for queries — two pools opening connections to the same
database is ordinary. It is **not** safe for failover: two managers on a
five-second tick, each seeing no healthy primary, can both promote.

So `FailoverManager.Start` checks `m.owned()` before each `monitor` tick
(`server/internal/orchestration/ownership.go`). The predicate is installed by the
registry from an advisory lock. **Default is owned** — a deployment that never
turned this on has one process, and making it prove that first would silently
stop failover for everyone.

## Rules that must not regress

- **flock, not a pid file.** The kernel releases it however the holder dies. A
  claim written to a file survives `kill -9` and locks out every later process
  until someone deletes it by hand. Pinned by
  `TestOrchestrationLockSurvivesAKilledHolder`, which kills a real child.
- **Scoped per data directory.** That is what an in-place upgrade shares, and what
  two Pontus instances managing different clusters do not — so those both keep
  orchestrating.
- **A relative lock path is refused.** `system.GetDatabasePath` falls back to a
  bare filename when it cannot create its directory; two processes with different
  working directories would then lock different files and both believe they hold
  it. It also dropped a lock file into the repo during a test, which is how it was
  found.
- **The lock is released in `StopAll`, before draining finishes**, so a waiting
  process takes over while the old one drains rather than after it exits.
- **Non-blocking acquire.** A process that cannot get the lock still serves
  queries; it just does not act on the cluster.
- **Unix only.** Windows' `SO_REUSEADDR` lets an unrelated process *take over* a
  bound port rather than share it, so mapping onto it would be a hijacking
  primitive. That platform refuses.

## What the test asserts, and what it does not

`e2e/upgrade_test.go` runs connections continuously while a second process binds
the same address and the first exits, and asserts **none is refused**. It
deliberately does not assert a distribution: Linux (3.9+) balances new connections
across the sockets, Darwin and the BSDs hand them all to the most recent binder.
Both are fine for an upgrade, and pinning one would fail on the other platform for
no reason that matters. An earlier version of the unit test did exactly that.

## The cost

`reuse_port` gives up the "address already in use" guard. A second Pontus started
by mistake — a stale unit file, a duplicated deploy — no longer fails; it silently
takes a share of the traffic. That is why it is off by default.

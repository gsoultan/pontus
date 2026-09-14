# Production readiness plan

Target: **PostgreSQL-only v1.0.0**, self-operated, with automatic failover proven
in CI on every commit. Scope decided 2026-09-13.

Baseline measured the same day: `go build ./...` clean, `gofmt`/`go vet` clean,
`go test -race ./...` 26 ok / 0 fail, zero `TODO`/`FIXME` in non-test Go.
What follows is what stands between that and running it on traffic that matters.

Out of scope for 1.0, deliberately: MySQL, Raft-replicated management state,
`ScheduleMaintenance`, accurate classification of statements over 32 KB, failback.

---

## Track A — Stop claiming what the code does not do (1–2 days)

**A1. Gate MySQL behind an explicit opt-in.**
`server/management/infrastructure/registry/registry.go:150` selects the handler on
a `switch` whose `default:` is Postgres, and `pkg/config` never validates
`Protocol` at all. Two consequences: `protocol: mysql` silently selects a handler
whose `GetCurrentLSN`, `WaitLSN`, `ReplayPreparedStatements` and `DiscoverTopology`
return nil — read-your-writes consistency quietly does nothing — and
`protocol: postgre` (a typo) silently gets Postgres rather than an error.

- Validate `Protocol` in `pkg/config` at load: `postgres` or `mysql`, anything
  else is a startup error naming the value.
- `mysql` requires `experimental_mysql: true` or the process refuses to start,
  with the four unimplemented methods named in the message.
- Make the registry `switch` explicit: `case "postgres"`, `case "mysql"`,
  `default:` returns an error rather than guessing.
- README feature #1 becomes "PostgreSQL. MySQL/MariaDB is experimental."

*Done when:* a config with `protocol: mysql` and no flag fails to start, a typo'd
protocol fails to start, and `pkg/config`'s wiring test covers both.

**A2. Correct the three stale Serena memories** so the next audit does not
re-litigate settled ground: `mem:security` Boundary 1 still says client TLS is
unwired and auth is HS256 (it is wired; auth is PASETO v4.local); `mem:failover`
"Still open" still says there is no two-backend E2E topology (there is);
`mem:findings` C8 is fixed at `gateway.go:954`.

---

## Track B — Prove the blast radius (1 week)

Automatic failover is the highest-consequence feature and the least-tested on the
code you actually ship. `TestLocalAutomaticFallback` passes on a laptop and never
runs in CI.

**B1. Make the local-cluster suite run in CI.** `requireLocalPostgres`
(`e2e/local_cluster_test.go:50`) skips unless `initdb`, `pg_ctl`, `pg_basebackup`
and `postgres` are on `PATH`; `.github/workflows/ci.yml` adds only Go and node
bins. The agent is built by the harness itself (`buildLocalAgent`), so this is:

```yaml
- name: PostgreSQL server binaries
  run: echo "/usr/lib/postgresql/16/bin" >> "$GITHUB_PATH"
- name: Go E2E
  env:
    PONTUS_E2E_BACKEND: 127.0.0.1:55832
    PONTUS_E2E_REPLICA: 127.0.0.1:55833
    PONTUS_E2E_DISRUPTIVE: "1"
  run: go test -tags e2e -timeout 30m ./e2e/...
```

**B2. Budget for what turns green-to-red.** These tests have never run on CI
hardware. The harness waits on 60s deadlines and `100ms` poll loops written
against a fast local machine; expect timing failures that are the *test's* fault
and at least one that is not. Do not raise a timeout to make a failure go away
without naming what it was waiting for.

**B3. Un-skip the two agent-dependent tests.** `e2e/promotion_test.go:48` and
`e2e/rejoin_test.go:67` skip on "needs a disposable cluster with agents" — they
were written against the container harness, where PostgreSQL is PID 1 and
stopping it kills the co-located agent. Point them at `startLocalCluster`, which
exists precisely to solve that.

**B4. Add a split-brain test.** Two Pontus processes, both seeing no healthy
primary, must produce exactly one promotion. Both guards need exercising: the
`pkg/listen` orchestration lock (same host, overlapping `reuse_port` upgrade) and
Raft (different hosts). Today `consensus.enabled: false` is the default, so the
single-host lock is the one carrying production, and it has no test that races it.

*Done when:* kill-the-primary, rejoin, primary-loss and split-brain all run on
every push, and a deliberate regression in `FailoverManager.monitor` turns CI red.

---

## Track C — Cover the code that deletes data (1.5 weeks)

Eleven packages sit at 0.0%, and they are not the harmless ones. Ordered by blast
radius, not by size:

| # | Package | Why it is first |
| :--- | :--- | :--- |
| 1 | `agent/infrastructure/validator` | "a validator that reports healthy on error" is an `agent` veto in AGENTS.md, and its verdict gates operations that erase a data directory |
| 2 | `internal/app` | config load, store construction, migrations, service install — a bad migration here is the one failure with no rollback |
| 3 | `management/infrastructure/manager` | the largest untested package; it drives every agent RPC |
| 4 | `management/service`, `management/state`, `pkg/repository` | the layer between the API and the store |
| 5 | `server/internal/replication` | slot management against a live primary |
| 6 | `management/handler` (2.1%) | thin, but it is the authorisation boundary's caller |

The target is not a coverage percentage. It is: **every path that writes to disk,
deletes a directory, or issues an agent RPC has a test, and every one of them has
a test for its failure branch returning an error rather than a zero value.**

---

## Track D — Operability, because you are the one on call (1 week)

**D1. A rollback path.** `reuse_port` gives a clean forward upgrade
(`mem:zero_downtime_upgrade`). There is no documented reverse. SQLite migrations
are forward-only, so a downgrade after a migration has run is the scenario that
strands a self-operated deployment. Decide and document one of: migrations are
backward-compatible for N-1, or a downgrade requires a restore, and the backup is
taken automatically before a migration runs.

**D2. Prove migration idempotency** on a fresh *and* a populated DB — AGENTS.md's
`data` Proof column asks for exactly this and nothing enforces it.

**D3. One runbook page per failure mode**, each naming the metric that fires and
the command that resolves it: primary loss · replica lag with a healthy stream ·
replica cut off from its primary (reports zero lag — the trap in AGENTS.md) ·
pool exhaustion · agent unreachable · suspected stale cache · control-plane node
loss.

**D4. Alert on `pontus_pool_identity_mismatches_total`.** AGENTS.md says it should
be zero; anything else means a pool is handing out a connection it does not own,
which is the cross-user finding (A11) recurring. Also audit the 30 metric families
for label cardinality before they meet a real query mix.

---

## Track E — Find the ceiling before production does (3–4 days)

**E1. 24-hour soak** at realistic concurrency. Watch RSS, goroutine count, pool
occupancy, and specifically the bounded maps: `maxSessionVars` (64),
`maxSessionStmts` (256), tenant limiters (`DefaultMaxTenants` 4096). Each has a
stated bound; confirm the bound is reached gracefully rather than theorised.

**E2. benchstat against pgbouncer** on the same box, same workload. Not for a
README number — so that when something is slow you know whether Pontus is it.

**E3. Measure the 32 KB ceiling.** `pkg/buffer.DefaultBufferSize` is 32 KB, and a
statement split across reads is never reassembled. It fails safe — unreadable
means not-read-only means primary, uncached — so it is a performance and
routing-accuracy issue, not a correctness one. Count how often it fires in your
workload. If it is common, the gateway framing loop moves from "known limitation"
into Track B.

---

## Sequence

| Week | Work |
| :--- | :--- |
| 1 | A1, A2 → B1, B2 |
| 2 | B3, B4 → C1, C2 |
| 3 | C3–C6 |
| 4 | D1–D4 |
| 5 | E1–E3 → tag `v1.0.0` |

Tracks C and D can run in parallel with a second pair of hands. B blocks nothing
but should not be deferred: every week it is not in CI is a week of commits landing
against an unverified failover path.

## Go / no-go for v1.0.0

- [ ] `protocol: mysql` refuses to start without the experimental flag
- [ ] kill-the-primary, rejoin, primary-loss and split-brain run on every push
- [ ] no package that writes to disk or drives an agent sits at 0% coverage
- [ ] a documented, tested downgrade path exists
- [ ] a runbook entry for every failure mode with an alert
- [ ] 24h soak clean, with numbers

# Storage engine decisions (benchmarked 2026-08-07)

## Management store stays SQLite. PostgreSQL is not an option.

Not "overkill" — **wrong**. Pontus is the proxy in front of PostgreSQL; if the
control plane needs a PostgreSQL to boot, you cannot use Pontus to reach
PostgreSQL, and the dashboard is down during exactly the incident it exists for.
It also breaks the one-binary product and `CGO_ENABLED=0`.

Measured volumes are trivial: `management.db` holds projects/users/settings as
JSON blobs keyed by id; metrics are **1 snapshot/min + 20 top-query rows/min**
(`tracker.go` ticker), so 7-day retention is ~10k and ~200k rows.

**Real gap, not solved by Postgres:** the Raft FSM (`consensus/node.go`)
replicates only `Backends` and `Config`, in memory with file snapshots. It does
**not** replicate `management.db`, so projects/users/settings diverge per node
in a multi-node control plane. Fix is to route that state through the existing
FSM.

## Logs stay SQLite. Pebble was benchmarked and rejected.

100k log-shaped entries, `GOMAXPROCS=2`, pure Go, Go 1.26:

| Engine | rows/sec | range+100 | level+100 | search | count | heap |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite per-row *(old)* | 39,716 | 135µs | **12.6ms** | 361µs | 1.36ms | 52MB |
| SQLite batched | **425,854** | **100µs** | **267µs** | **302µs** | **1.17ms** | 52MB |
| SQLite batched + FTS5 | 177,684 | 99µs | 271µs | 14.0ms | 1.21ms | 52MB |
| Pebble (LSM) | 690,019 | 306µs | 1.11ms | 972µs | 3.19ms | 64MB |

Pebble wins writes 1.6× over batched SQLite but loses **every** query shape
3–4×, costs +12MB heap, runs background compaction competing for 2 cores, and
has no SQL aggregation or text search. The write headroom is unusable — Pontus
will never emit 425k lines/sec. **The bottleneck was never SQLite; it was one
transaction per row.**

Three results that contradict intuition — do not "optimize" these back:

1. **`idx_logs_level` was harmful.** ~5 distinct values misled the planner into
   an index scan: 12.6ms → 267µs once dropped. Dropped in `initSchema`.
   Same reasoning dropped `idx_queries_query` (indexed unbounded client SQL).
2. **FTS5 is not a default win** — 14ms vs 302µs for LIKE. `ORDER BY timestamp
   DESC LIMIT 100` makes scan-and-stop beat match-then-sort for common terms.
   FTS5 *is* available under `CGO_ENABLED=0` (verified, SQLite 3.53.3) — worth
   revisiting only for selective search on much larger tables.
3. WAL bloat from per-row inserts is **transient**, not steady-state storage
   (22MB vs 18MB after checkpoint). The throughput penalty is the real cost.

## Live logs are push, not polling

`StreamLogs` + `LogBroadcaster` fan out in memory, so live logs never touch the
DB — polling would add a query per client per interval. Backpressure is
drop-on-full at both the persistence channel and each subscriber. Persistence is
batched (256 entries / 500ms) in `runPersistenceWorker`.

## Metrics must be hydrated, not just cached

The in-memory ring is a **cache over the metric store**, not a second source of
truth. `SetStore` hydrates the 60-minute trend ring and lifetime counters;
without it a restart blanked the chart for an hour while the data sat on disk.
Snapshots record **windowed deltas**, not `total/uptime` — a lifetime average
flattens out and hides the spikes the chart exists to show. See
`mem:observability_pipeline` if that file exists, otherwise `tracker.go`.

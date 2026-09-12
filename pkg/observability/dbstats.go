package observability

import (
	"sync"
	"sync/atomic"
	"time"
)

// Per-database counters, for the figures pgbouncer's SHOW STATS reports.
//
// Prometheus counters cannot serve this: a CounterVec is write-only from the
// process's point of view, and the admin console has to read the numbers back
// to answer a query. These are the readable copy.
//
// A session resolves its bucket once and holds the pointer, so recording a
// query is a handful of atomic adds with no map lookup and no lock. That is the
// difference between instrumentation and a contended mutex on the query path.

// DatabaseStats accumulates what one database has done.
type DatabaseStats struct {
	queries      atomic.Int64
	transactions atomic.Int64
	received     atomic.Int64
	sent         atomic.Int64
	queryTime    atomic.Int64
}

// RecordQuery accounts for one statement and the time it took.
//
// endedTransaction marks the statement that returned the session to idle, which
// is what pgbouncer counts as a transaction: an explicit one when it commits,
// and every standalone statement, since each is its own implicit transaction.
func (d *DatabaseStats) RecordQuery(elapsed time.Duration, received, sent int64, endedTransaction bool) {
	if d == nil {
		return
	}
	d.queries.Add(1)
	d.queryTime.Add(int64(elapsed))
	d.received.Add(received)
	d.sent.Add(sent)
	if endedTransaction {
		d.transactions.Add(1)
	}
}

// DatabaseStat is one database's totals, read out for reporting.
type DatabaseStat struct {
	Database     string
	Queries      int64
	Transactions int64
	Received     int64
	Sent         int64
	QueryTime    time.Duration
}

// MaxTrackedDatabases bounds the registry.
//
// The key is the database name from a startup packet, which is client-supplied:
// without a bound this is a map an unauthenticated peer can grow by reconnecting
// with a new name each time. Past the bound everything accumulates into one
// bucket, so the totals stay right even though the attribution stops.
const MaxTrackedDatabases = 256

// OverflowDatabase is the name reported for everything past the bound.
const OverflowDatabase = "(other)"

// DatabaseRegistry holds one bucket per database.
type DatabaseRegistry struct {
	mu       sync.RWMutex
	byName   map[string]*DatabaseStats
	overflow DatabaseStats
	since    time.Time
}

func NewDatabaseRegistry() *DatabaseRegistry {
	return &DatabaseRegistry{
		byName: make(map[string]*DatabaseStats),
		since:  time.Now(),
	}
}

// For returns the bucket a session should record into.
//
// Called once per session, not once per query. The read lock is taken on the
// common path where the database is already known, and the write lock only when
// a name is seen for the first time.
func (r *DatabaseRegistry) For(database string) *DatabaseStats {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	stats, ok := r.byName[database]
	r.mu.RUnlock()
	if ok {
		return stats
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if stats, ok := r.byName[database]; ok {
		return stats
	}
	if len(r.byName) >= MaxTrackedDatabases {
		return &r.overflow
	}

	stats = &DatabaseStats{}
	r.byName[database] = stats
	return stats
}

// Snapshot reads every bucket. Ordered by the caller, not here: the map has no
// order and pretending otherwise would make repeated calls disagree.
func (r *DatabaseRegistry) Snapshot() []DatabaseStat {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]DatabaseStat, 0, len(r.byName)+1)
	for name, stats := range r.byName {
		out = append(out, stats.read(name))
	}
	if overflow := r.overflow.read(OverflowDatabase); overflow.Queries > 0 {
		out = append(out, overflow)
	}
	return out
}

// Since is when these totals started accumulating, which is what turns them
// into the rates SHOW STATS reports as averages.
func (r *DatabaseRegistry) Since() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.since
}

func (d *DatabaseStats) read(name string) DatabaseStat {
	return DatabaseStat{
		Database:     name,
		Queries:      d.queries.Load(),
		Transactions: d.transactions.Load(),
		Received:     d.received.Load(),
		Sent:         d.sent.Load(),
		QueryTime:    time.Duration(d.queryTime.Load()),
	}
}

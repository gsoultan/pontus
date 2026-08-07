package middleware

import (
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// DefaultMaxTenants bounds how many per-user limiters are retained.
//
// The map is keyed by the username a client supplies at startup, so it is
// attacker-controlled. Without a cap, a client reconnecting under fresh
// usernames grows it without limit — a remote OOM rather than a rate limit.
const DefaultMaxTenants = 4096

// tenantIdleTimeout is how long an unused limiter survives a sweep.
const tenantIdleTimeout = 10 * time.Minute

type tenantEntry struct {
	limiter *rate.Limiter
	// lastSeen is unix nanos, updated without a lock on the hit path.
	lastSeen atomic.Int64
}

// TenantLimiters holds per-user rate limiters in a bounded, self-evicting map.
//
// Lookups of a known tenant take the sync.Map fast path with no lock and no
// allocation. Admitting an *unknown* tenant is serialized: the bound has to
// hold under a flood of unique usernames, which is precisely when many
// goroutines miss at once. Serializing admission also throttles that flood,
// while steady-state traffic — a bounded set of real tenants — never touches
// the mutex.
type TenantLimiters struct {
	entries sync.Map // string -> *tenantEntry

	admitMu sync.Mutex // guards admission, eviction and count
	count   int

	max   int
	limit rate.Limit
	burst int
}

// NewTenantLimiters builds a limiter set from the configured rate and burst.
// A non-positive max falls back to DefaultMaxTenants.
func NewTenantLimiters(limit rate.Limit, burst, max int) *TenantLimiters {
	if max <= 0 {
		max = DefaultMaxTenants
	}
	return &TenantLimiters{max: max, limit: limit, burst: burst}
}

// Get returns the limiter for a tenant, creating one if needed.
func (t *TenantLimiters) Get(tenant string) *rate.Limiter {
	now := time.Now().UnixNano()

	if value, ok := t.entries.Load(tenant); ok {
		entry := value.(*tenantEntry)
		entry.lastSeen.Store(now)
		return entry.limiter
	}

	return t.admit(tenant, now)
}

func (t *TenantLimiters) admit(tenant string, now int64) *rate.Limiter {
	t.admitMu.Lock()
	defer t.admitMu.Unlock()

	// Another goroutine may have admitted this tenant while we waited.
	if value, ok := t.entries.Load(tenant); ok {
		entry := value.(*tenantEntry)
		entry.lastSeen.Store(now)
		return entry.limiter
	}

	if t.count >= t.max {
		t.evictLocked(now)
	}

	entry := &tenantEntry{limiter: rate.NewLimiter(t.limit, t.burst)}
	entry.lastSeen.Store(now)
	t.entries.Store(tenant, entry)
	t.count++
	return entry.limiter
}

// evictLocked frees at least one slot. Callers must hold admitMu.
func (t *TenantLimiters) evictLocked(now int64) {
	cutoff := now - tenantIdleTimeout.Nanoseconds()

	removed := 0
	oldest := int64(0)
	var oldestKey any

	t.entries.Range(func(key, value any) bool {
		entry := value.(*tenantEntry)
		seen := entry.lastSeen.Load()
		if seen < cutoff {
			t.entries.Delete(key)
			removed++
			return true
		}
		if oldestKey == nil || seen < oldest {
			oldest, oldestKey = seen, key
		}
		return true
	})

	// Every tenant is active and the map is still full: drop the
	// least-recently-used one so admission still makes progress. This is a
	// rate limiter, not an accounting ledger — a bounded map that occasionally
	// forgets an active tenant is correct; an unbounded one is an outage.
	if removed == 0 && oldestKey != nil {
		t.entries.Delete(oldestKey)
		removed++
	}

	t.count -= removed
	if t.count < 0 {
		t.count = 0
	}
}

// Len reports the number of retained tenants. Test and metrics use only.
func (t *TenantLimiters) Len() int {
	t.admitMu.Lock()
	defer t.admitMu.Unlock()
	return t.count
}

package cache

import (
	"strconv"
	"testing"
	"time"
)

// A write must evict what it invalidates. Invalidate previously had no caller at
// all, so a cached SELECT kept serving pre-write rows for the whole TTL.
func TestManager_InvalidateEvictsByTable(t *testing.T) {
	m := NewManager()
	m.Set("k1", []byte("v1"), time.Minute, []string{"orders"})
	m.Set("k2", []byte("v2"), time.Minute, []string{"invoices"})

	m.Invalidate([]string{"orders"})

	if _, hit, _ := m.Get("k1"); hit {
		t.Error("entry reading from orders survived an invalidation of orders")
	}
	if _, hit, _ := m.Get("k2"); !hit {
		t.Error("entry reading from invoices was evicted by an unrelated table")
	}
}

// An entry belonging to several tables must be dropped from all of their index
// sets, not just the one being invalidated, or the index keeps growing with
// references to items that no longer exist.
func TestManager_InvalidateDoesNotOrphanIndex(t *testing.T) {
	m := NewManager()
	m.Set("k1", []byte("v"), time.Minute, []string{"a", "b"})
	m.Invalidate([]string{"a"})

	m.mu.RLock()
	_, stillIndexed := m.tables["b"]["k1"]
	m.mu.RUnlock()

	if stillIndexed {
		t.Error("key remained in table b's index after being deleted from items")
	}
}

// Re-storing an entry must carry its table associations, or the refreshed copy
// becomes permanently stale — no write can ever evict it again.
func TestManager_ResetKeepsInvalidationReachable(t *testing.T) {
	m := NewManager()
	m.Set("k", []byte("v1"), time.Minute, []string{"t"})
	m.Set("k", []byte("v2"), time.Minute, []string{"t"}) // what a refresh does

	m.Invalidate([]string{"t"})
	if _, hit, _ := m.Get("k"); hit {
		t.Error("refreshed entry survived invalidation of its own table")
	}
}

// The map is keyed by client-supplied query text, so the bound is what stops a
// client deciding how much memory the proxy holds.
func TestManager_EnforcesMaxSize(t *testing.T) {
	const maxSize = 32
	m := NewManagerWithSize(maxSize)

	for i := range 500 {
		m.Set("key-"+strconv.Itoa(i), []byte("value"), time.Minute, []string{"t"})
	}

	if _, _, count := m.Stats(); count > maxSize {
		t.Errorf("cache holds %d entries, exceeds max size %d", count, maxSize)
	}
}

func TestManager_CleanupDropsFullyExpired(t *testing.T) {
	m := NewManager()
	m.Set("k", []byte("v"), 10*time.Millisecond, []string{"t"})

	time.Sleep(40 * time.Millisecond) // past Expiration and past StaleUntil (2x TTL)
	m.Cleanup()

	if _, _, count := m.Stats(); count != 0 {
		t.Errorf("expired entry survived cleanup: %d entries remain", count)
	}
}

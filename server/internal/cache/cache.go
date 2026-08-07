package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// DefaultMaxSize bounds the cache when the configuration does not. The map is
// keyed by client-supplied query text, so "no limit" means a client chooses how
// much memory the proxy holds.
const DefaultMaxSize = 10000

// Item represents a cached result set.
type Item struct {
	Value      []byte
	Expiration time.Time
	StaleUntil time.Time
	Tables     []string
}

// Manager handles result set caching and invalidation.
type Manager struct {
	mu      sync.RWMutex
	items   map[string]Item
	tables  map[string]map[string]struct{} // table -> set of keys
	maxSize int
	hits    atomic.Int64
	misses  atomic.Int64
	evicted atomic.Int64
}

// NewManager creates a new cache manager bounded to DefaultMaxSize.
func NewManager() *Manager {
	return NewManagerWithSize(DefaultMaxSize)
}

// NewManagerWithSize creates a cache manager holding at most maxSize entries.
func NewManagerWithSize(maxSize int) *Manager {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	return &Manager{
		items:   make(map[string]Item),
		tables:  make(map[string]map[string]struct{}),
		maxSize: maxSize,
	}
}

// Get retrieves a value from the cache.
func (m *Manager) Get(key string) (value []byte, hit bool, needsRevalidate bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	item, ok := m.items[key]
	if !ok {
		m.misses.Add(1)
		return nil, false, false
	}

	now := time.Now()
	if now.After(item.Expiration) {
		if now.Before(item.StaleUntil) {
			m.hits.Add(1)
			return item.Value, true, true
		}
		m.misses.Add(1)
		return nil, false, false
	}

	m.hits.Add(1)
	return item.Value, true, false
}

// Set stores a value in the cache.
//
// tables is what makes write invalidation possible; storing an entry without it
// creates one that no write can ever evict.
func (m *Manager) Set(key string, value []byte, ttl time.Duration, tables []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Replacing an existing key must drop its old table associations, or the
	// index keeps pointing at tables the new value does not depend on.
	if old, ok := m.items[key]; ok {
		m.unindex(key, old.Tables)
	}

	if len(m.items) >= m.maxSize {
		m.evictLocked()
	}

	now := time.Now()
	m.items[key] = Item{
		Value:      value,
		Expiration: now.Add(ttl),
		StaleUntil: now.Add(ttl * 2), // Allow stale for twice the TTL
		Tables:     tables,
	}

	for _, table := range tables {
		if m.tables[table] == nil {
			m.tables[table] = make(map[string]struct{})
		}
		m.tables[table][key] = struct{}{}
	}
}

// Invalidate evicts every entry that reads from any of the given tables.
func (m *Manager) Invalidate(tables []string) {
	if len(tables) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, table := range tables {
		keys, ok := m.tables[table]
		if !ok {
			continue
		}

		for key := range keys {
			// Drop the key from *all* of its tables, not just this one. Deleting
			// only this table's set left entries in the other tables' sets
			// pointing at an item that no longer exists, and those sets grew
			// without bound.
			if item, exists := m.items[key]; exists {
				m.unindex(key, item.Tables)
				delete(m.items, key)
			}
		}
		delete(m.tables, table)
	}
}

// Cleanup removes entries that are past even their stale window.
func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for key, item := range m.items {
		if now.After(item.StaleUntil) {
			m.unindex(key, item.Tables)
			delete(m.items, key)
		}
	}
}

// StartJanitor prunes expired entries until stop is closed. Without it the map
// only shrinks when a write happens to invalidate the right table.
func (m *Manager) StartJanitor(interval time.Duration, stop <-chan struct{}) {
	if interval <= 0 {
		interval = time.Minute
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.Cleanup()
			case <-stop:
				return
			}
		}
	}()
}

// evictLocked makes room for one entry. Expired entries go first; if none are
// expired the entry closest to expiry is dropped, so the bound holds regardless.
func (m *Manager) evictLocked() {
	now := time.Now()
	for key, item := range m.items {
		if now.After(item.StaleUntil) {
			m.unindex(key, item.Tables)
			delete(m.items, key)
			m.evicted.Add(1)
			return
		}
	}

	var oldestKey string
	var oldest time.Time
	for key, item := range m.items {
		if oldest.IsZero() || item.Expiration.Before(oldest) {
			oldestKey, oldest = key, item.Expiration
		}
	}
	if oldestKey != "" {
		m.unindex(oldestKey, m.items[oldestKey].Tables)
		delete(m.items, oldestKey)
		m.evicted.Add(1)
	}
}

// unindex removes key from the table index. Callers hold mu.
func (m *Manager) unindex(key string, tables []string) {
	for _, table := range tables {
		keys, ok := m.tables[table]
		if !ok {
			continue
		}
		delete(keys, key)
		if len(keys) == 0 {
			delete(m.tables, table)
		}
	}
}

// Stats returns cache statistics.
func (m *Manager) Stats() (hits int64, misses int64, count int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hits.Load(), m.misses.Load(), len(m.items)
}

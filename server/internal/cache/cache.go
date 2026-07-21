package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// Item represents a cached result set.
type Item struct {
	Value      []byte
	Expiration time.Time
	StaleUntil time.Time
	Tables     []string
}

// Manager handles result set caching and invalidation.
type Manager struct {
	mu     sync.RWMutex
	items  map[string]Item
	tables map[string]map[string]struct{} // table -> set of keys
	hits   atomic.Int64
	misses atomic.Int64
}

// NewManager creates a new cache manager.
func NewManager() *Manager {
	return &Manager{
		items:  make(map[string]Item),
		tables: make(map[string]map[string]struct{}),
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
func (m *Manager) Set(key string, value []byte, ttl time.Duration, tables []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

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

// Invalidate evicts all keys associated with the given tables.
func (m *Manager) Invalidate(tables []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, table := range tables {
		keys, ok := m.tables[table]
		if !ok {
			continue
		}

		for key := range keys {
			delete(m.items, key)
		}
		delete(m.tables, table)
	}
}

// Cleanup removes expired items.
func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for key, item := range m.items {
		if now.After(item.StaleUntil) {
			m.removeKey(key, item.Tables)
		}
	}
}

func (m *Manager) removeKey(key string, tables []string) {
	delete(m.items, key)
	for _, table := range tables {
		if keys, ok := m.tables[table]; ok {
			delete(keys, key)
			if len(keys) == 0 {
				delete(m.tables, table)
			}
		}
	}
}

// Stats returns cache statistics.
func (m *Manager) Stats() (hits int64, misses int64, count int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hits.Load(), m.misses.Load(), len(m.items)
}

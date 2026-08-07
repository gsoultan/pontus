// Package replication tracks live replication (CDC) streams.
//
// A stream is not a pooled session. It is pinned to the node holding its slot,
// it occupies a pool permit for its whole lifetime, and it cannot move during
// failover. The registry exists so the control plane can show that difference
// rather than folding streams into a connection count where they look like
// ordinary traffic that will be released shortly.
package replication

import (
	"sync"
	"time"
)

// Stream is one attached replication consumer.
type Stream struct {
	ID           string
	SlotName     string
	ClientAddr   string
	BackendAddr  string
	Database     string
	User         string
	Kind         string // "logical" or "physical"
	Plugin       string
	StartedAt    time.Time
	LagBytes     int64
	LagMs        int64
	ConfirmedLSN string
}

// Registry holds the streams currently attached to a proxy.
//
// It is deliberately a plain in-memory map: a stream only exists while its
// connection does, so there is nothing to persist. Rebuilding it from the
// database on restart would describe consumers this process is not serving.
type Registry struct {
	mu      sync.RWMutex
	streams map[string]*Stream
	budget  int
}

// NewRegistry returns a registry with the configured stream budget.
//
// The budget is separate from max_conns on purpose. A CDC consumer holds its
// permit for hours, so without a ceiling of its own a handful of consumers
// would quietly consume the pool the application depends on. Exhausting this
// budget must reject the new consumer, never the application.
func NewRegistry(budget int) *Registry {
	return &Registry{streams: make(map[string]*Stream), budget: budget}
}

// Budget reports the configured ceiling.
func (r *Registry) Budget() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.budget
}

// SetBudget updates the ceiling. Lowering it below the number of attached
// streams does not disconnect anyone; it only stops new consumers attaching.
func (r *Registry) SetBudget(n int) {
	r.mu.Lock()
	r.budget = n
	r.mu.Unlock()
}

// Used reports how many streams are attached.
func (r *Registry) Used() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.streams)
}

// Add registers a stream. It reports false when the budget is exhausted, in
// which case the caller must refuse the consumer rather than borrow from the
// session pool.
func (r *Registry) Add(s *Stream) bool {
	if s == nil || s.ID == "" {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.streams[s.ID]; !exists && len(r.streams) >= r.budget {
		return false
	}
	r.streams[s.ID] = s
	return true
}

// Remove deregisters a stream.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.streams, id)
	r.mu.Unlock()
}

// List returns a snapshot, copied so callers cannot mutate live entries.
func (r *Registry) List() []Stream {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Stream, 0, len(r.streams))
	for _, s := range r.streams {
		out = append(out, *s)
	}
	return out
}

// CountFor returns how many streams are attached to one backend, for the
// per-node capacity meter.
func (r *Registry) CountFor(backendAddr string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var n int
	for _, s := range r.streams {
		if s.BackendAddr == backendAddr {
			n++
		}
	}
	return n
}

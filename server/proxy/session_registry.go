package proxy

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// sessionInfo describes one live client session.
type sessionInfo struct {
	id       uint64
	user     string
	database string
	addr     string
	since    time.Time
}

// sessionRegistry tracks live client sessions so the administration console can
// report who is connected.
//
// Bounded by accepted connections rather than by anything a client sends: one
// entry per TCP connection, removed when that connection closes. That is the
// distinction between this and a map keyed by a startup parameter, which is
// client-supplied and needs a cap and an eviction of its own.
//
// Registration happens once per session, not once per statement, so nothing
// here is on the query path.
type sessionRegistry struct {
	mu       sync.RWMutex
	sessions map[uint64]sessionInfo
	nextID   atomic.Uint64
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: make(map[uint64]sessionInfo)}
}

// add records a session and returns the handle that removes it.
func (r *sessionRegistry) add(user, database, addr string) uint64 {
	id := r.nextID.Add(1)

	r.mu.Lock()
	r.sessions[id] = sessionInfo{
		id:       id,
		user:     user,
		database: database,
		addr:     addr,
		since:    time.Now(),
	}
	r.mu.Unlock()
	return id
}

func (r *sessionRegistry) remove(id uint64) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

// list returns the live sessions, ordered by the sequence they were accepted
// in so repeated calls read consistently rather than in map order.
func (r *sessionRegistry) list() []sessionInfo {
	r.mu.RLock()
	out := make([]sessionInfo, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	r.mu.RUnlock()

	slices.SortFunc(out, func(a, b sessionInfo) int { return cmp.Compare(a.id, b.id) })
	return out
}

func (r *sessionRegistry) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

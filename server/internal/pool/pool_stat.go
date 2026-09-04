package pool

import "time"

// PoolStat is one identity's occupancy on one backend.
//
// Reported rather than summed, because "how full is the pool" is the wrong
// question when pools are keyed by (backend, database, user): a backend can sit
// at half its total ceiling while one tenant's own pool is full and every one
// of its sessions is queuing. Summing hides exactly the case an operator opened
// the console to find.
type PoolStat struct {
	// Database and User name the identity. Both are empty for the system
	// identity Pontus uses for its own health probes and role detection.
	Database string
	User     string

	Active   int32
	Idle     int32
	Total    int32
	Waiting  int32
	MaxConns int32

	// EmptyAcquires counts acquisitions that found no idle connection and had
	// to wait, and AcquireWait is the time they spent waiting. The mean of the
	// two is pgbouncer's avg_wait_time — the number that says a pool is too
	// small, which occupancy alone never does.
	EmptyAcquires int64
	AcquireWait   time.Duration
}

// AverageWait is the mean time an acquisition that had to wait spent waiting.
func (p PoolStat) AverageWait() time.Duration {
	if p.EmptyAcquires <= 0 {
		return 0
	}
	return p.AcquireWait / time.Duration(p.EmptyAcquires)
}

// stats returns one row per identity holding a pool on this backend.
func (s *poolSet) stats() []PoolStat {
	s.mu.Lock()
	out := make([]PoolStat, 0, len(s.pools))
	ids := make([]identity, 0, len(s.pools))
	cores := make([]*poolEntry, 0, len(s.pools))
	for id, entry := range s.pools {
		ids = append(ids, id)
		cores = append(cores, entry)
	}
	s.mu.Unlock()

	// Stat() is taken outside the set's lock. It reads the engine's own
	// counters, and holding a lock the acquire path needs while doing it would
	// put the console in front of every session on this backend.
	for i, entry := range cores {
		st := entry.core.Stat()
		out = append(out, PoolStat{
			Database:      ids[i].database,
			User:          ids[i].user,
			Active:        st.ActiveConnections(),
			Idle:          st.IdleConnections(),
			Total:         st.TotalConnections(),
			Waiting:       st.WaitingAcquires(),
			MaxConns:      st.MaxConnections(),
			EmptyAcquires: st.EmptyAcquireCount(),
			AcquireWait:   st.AcquireDuration(),
		})
	}
	return out
}

// PoolStats returns one row per identity holding a pool on this backend.
//
// Introspection rather than routing, so it is deliberately not on the Backend
// interface: that interface is already far past the size the guidelines allow,
// and every mock in the tree would have to grow a method none of them need.
// Callers assert for it.
func (p *Server) PoolStats() []PoolStat {
	if p == nil || p.pools == nil {
		return nil
	}
	return p.pools.stats()
}

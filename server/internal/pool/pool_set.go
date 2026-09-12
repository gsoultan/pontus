package pool

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gsoultan/gpool/pkg/pooling"
)

// ErrBackendAtCapacity is returned when a backend already holds as many
// connections as it is allowed and none can be freed.
var ErrBackendAtCapacity = errors.New("backend is at its connection ceiling")

// identity names the credentials a connection was opened with.
//
// A connection carries the credentials it authenticated with and cannot
// renegotiate them, so it is only interchangeable between sessions that share
// both halves. Keying pools this way is what makes reuse safe; without it a
// pool keeps offering one user's connection to another, which was a live
// cross-user data path before it was caught (finding A11).
type identity struct {
	user     string
	database string
}

func (i identity) String() string {
	if i.user == "" && i.database == "" {
		return "system"
	}
	return i.user + "@" + i.database
}

// poolEntry is one identity's pool plus when it was last wanted.
type poolEntry struct {
	core     *pooling.Core[*Conn]
	lastUsed time.Time
}

// poolSet holds one connection pool per (user, database) on a single backend.
//
// Per-identity pools create a counting problem the single pool did not have.
// `max_conns` is now a *per-identity* ceiling, and the number of identities is
// driven by the user name in a startup packet — which is client-supplied. Per
// pool alone therefore has no upper bound at all: it is a map keyed by
// attacker-controlled input where every entry costs real connections on the
// database. maxTotal and maxPools are the other half of that decision, not
// optional extras.
type poolSet struct {
	mu    sync.Mutex
	pools map[identity]*poolEntry

	// newCore builds a pool for one identity.
	//
	// It takes the identity because the ceiling is not the same for every one
	// of them: a per-database `max_conns` is applied when the pool is built,
	// and a closure that could not see whose pool it was building would have to
	// give them all the global value.
	newCore func(identity) (*pooling.Core[*Conn], error)

	// maxTotal caps connections to this backend across every pool — pgbouncer's
	// max_db_connections, and what keeps Pontus inside the database's own
	// max_connections.
	maxTotal int32

	// maxPools bounds the map itself, so a user who connects once does not hold
	// a pool for the life of the process.
	maxPools int

	// idleTTL is how long an unused pool is kept before it is reaped.
	idleTTL time.Duration

	address string
	closed  bool
}

func newPoolSet(address string, maxTotal int32, maxPools int, idleTTL time.Duration,
	newCore func(identity) (*pooling.Core[*Conn], error)) *poolSet {
	return &poolSet{
		pools:    make(map[identity]*poolEntry),
		newCore:  newCore,
		maxTotal: maxTotal,
		maxPools: maxPools,
		idleTTL:  idleTTL,
		address:  address,
	}
}

// get returns the pool for an identity, creating it if there is room.
func (s *poolSet) get(id identity) (*pooling.Core[*Conn], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrPoolClosed
	}

	if entry, ok := s.pools[id]; ok {
		entry.lastUsed = time.Now()
		return entry.core, nil
	}

	// Reap on use rather than on a timer: it keeps the map tidy without a
	// goroutine that has to be shut down, and the cost lands on the caller that
	// is already paying for a new pool.
	s.reapLocked()

	if len(s.pools) >= s.maxPools && !s.evictIdlestLocked() {
		return nil, fmt.Errorf("%w: %d identity pools on %s and all are in use",
			ErrBackendAtCapacity, len(s.pools), s.address)
	}
	if err := s.ensureRoomLocked(); err != nil {
		return nil, err
	}

	core, err := s.newCore(id)
	if err != nil {
		return nil, fmt.Errorf("pool for %s on %s: %w", id, s.address, err)
	}
	s.pools[id] = &poolEntry{core: core, lastUsed: time.Now()}
	return core, nil
}

// ensureRoomLocked frees a connection slot when the backend is at its ceiling.
//
// Eviction takes the least recently used *idle* pool rather than refusing the
// new session: turning away a new user while idle connections sit unused for
// users who have gone is the wrong way to run out.
func (s *poolSet) ensureRoomLocked() error {
	if s.maxTotal <= 0 || s.totalLocked() < s.maxTotal {
		return nil
	}
	if s.evictIdlestLocked() {
		return nil
	}
	return fmt.Errorf("%w: %d connections on %s and none idle",
		ErrBackendAtCapacity, s.totalLocked(), s.address)
}

func (s *poolSet) totalLocked() int32 {
	var total int32
	for _, entry := range s.pools {
		total += entry.core.Stat().TotalConnections()
	}
	return total
}

// evictIdlestLocked closes the least recently used pool holding nothing
// checked out. Reports whether it freed one.
func (s *poolSet) evictIdlestLocked() bool {
	var victim identity
	var oldest time.Time
	found := false

	for id, entry := range s.pools {
		if entry.core.Stat().ActiveConnections() > 0 {
			continue // in use; taking it would break a live session
		}
		if !found || entry.lastUsed.Before(oldest) {
			victim, oldest, found = id, entry.lastUsed, true
		}
	}
	if !found {
		return false
	}

	slog.Debug("Evicting an idle connection pool",
		"backend", s.address, "identity", victim.String())
	s.pools[victim].core.Close()
	delete(s.pools, victim)
	return true
}

// reapLocked drops pools nobody has asked for in idleTTL.
func (s *poolSet) reapLocked() {
	if s.idleTTL <= 0 {
		return
	}
	cutoff := time.Now().Add(-s.idleTTL)
	for id, entry := range s.pools {
		if entry.lastUsed.After(cutoff) {
			continue
		}
		if entry.core.Stat().ActiveConnections() > 0 {
			continue
		}
		entry.core.Close()
		delete(s.pools, id)
	}
}

// each runs fn over every pool: resizing, eviction, statistics and shutdown all
// used to act on one core and now have to act on all of them.
func (s *poolSet) each(fn func(*pooling.Core[*Conn])) {
	s.eachIdentity(func(_ identity, core *pooling.Core[*Conn]) { fn(core) })
}

// eachIdentity is each, for the callers that need to know whose pool they are
// looking at — a resize that must respect a per-database ceiling, and the
// statistics the admin console reports per (database, user).
//
// fn runs outside the set's lock. It reads the engine's own counters or calls
// into it, and holding a lock the acquire path needs while doing that would put
// the caller in front of every session on this backend.
func (s *poolSet) eachIdentity(fn func(identity, *pooling.Core[*Conn])) {
	s.mu.Lock()
	ids := make([]identity, 0, len(s.pools))
	cores := make([]*pooling.Core[*Conn], 0, len(s.pools))
	for id, entry := range s.pools {
		ids = append(ids, id)
		cores = append(cores, entry.core)
	}
	s.mu.Unlock()

	for i, core := range cores {
		fn(ids[i], core)
	}
}

// poolTotals is the backend's occupancy summed across identities.
//
// Its own type because pooling.Stat has unexported fields and cannot be
// assembled outside its package — and because summing is the honest thing to
// report: the dashboard and the adaptive controller care about the backend, not
// about one identity's slice of it.
type poolTotals struct {
	Total    int32
	Idle     int32
	Active   int32
	Waiting  int32
	MaxConns int32
	Pools    int

	// EmptyAcquires and AcquireWait are cumulative across pools, so the mean
	// wait they produce is over every identity's blocked acquisitions rather
	// than one pool's.
	EmptyAcquires int64
	AcquireWait   time.Duration
}

func (s *poolSet) totals() poolTotals {
	var t poolTotals
	s.each(func(core *pooling.Core[*Conn]) {
		st := core.Stat()
		t.Total += st.TotalConnections()
		t.Idle += st.IdleConnections()
		t.Active += st.ActiveConnections()
		t.Waiting += st.WaitingAcquires()
		t.MaxConns += st.MaxConnections()
		t.EmptyAcquires += st.EmptyAcquireCount()
		t.AcquireWait += st.AcquireDuration()
	})
	t.Pools = s.count()
	return t
}

func (s *poolSet) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, entry := range s.pools {
		entry.core.Close()
		delete(s.pools, id)
	}
}

// mustGet is get, for callers that already know which pool they want.
func (s *poolSet) mustGet(id identity) (*pooling.Core[*Conn], error) { return s.get(id) }

func (s *poolSet) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pools)
}

// Bounds on the identity pool map.
//
// Both exist because the map is keyed by a user name from a startup packet,
// which anyone who can reach the port chooses. Neither is a tuning preference.
const (
	// maxIdentityPools caps how many (user, database) pools one backend keeps.
	// Generous, because a legitimate multi-tenant deployment has many; finite,
	// because the key is client-supplied.
	maxIdentityPools = 256

	// identityPoolTTL is how long a pool nobody has used is kept. A user who
	// connects once should not hold connections for the life of the process.
	identityPoolTTL = 5 * time.Minute

	// backendConnMultiplier turns the per-identity max_conns into a ceiling for
	// the whole backend when no explicit one is configured.
	//
	// Four is a guess and is meant to be replaced by max_backend_conns; what
	// matters is that some finite bound exists, because per-pool alone has none.
	backendConnMultiplier = 4
)

// backendConnCeiling derives the total connection ceiling for a backend.
func backendConnCeiling(perIdentity int32) int32 {
	if perIdentity <= 0 {
		return 0 // unbounded is the engine's own default; nothing to add here
	}
	return perIdentity * backendConnMultiplier
}

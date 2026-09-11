package pool

import (
	"sync"
	"time"
)

// Live backend connections, so the administration console can enumerate them.
//
// The pooling engine reports occupancy — how many are open, idle, waiting — and
// that is the right contract for it: capacity is its job. Which *individual*
// connection is in which state is not, and it is what an operator needs when a
// pool is full and they want to know what is holding it.
//
// Pontus already owns the connection type, precisely so it can keep
// per-connection state, so the registry is a set of those rather than anything
// the engine has to grow. It is populated where connections are born and die —
// the driver's Connect and Close — so it cannot drift from what is really open.
//
// Bounded by the pool ceilings above it: a connection exists only because a
// permit was granted, and `max_conns` per identity with a total per backend is
// what bounds those.

// connRegistry is the set of connections currently open to one backend.
type connRegistry struct {
	mu    sync.RWMutex
	conns map[*Conn]struct{}
}

func newConnRegistry() *connRegistry {
	return &connRegistry{conns: make(map[*Conn]struct{})}
}

func (r *connRegistry) add(c *Conn) {
	if r == nil || c == nil {
		return
	}
	r.mu.Lock()
	r.conns[c] = struct{}{}
	r.mu.Unlock()
}

func (r *connRegistry) remove(c *Conn) {
	if r == nil || c == nil {
		return
	}
	r.mu.Lock()
	delete(r.conns, c)
	r.mu.Unlock()
}

// list copies the set so the snapshot can be read without holding the lock
// while each connection is interrogated.
func (r *connRegistry) list() []*Conn {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Conn, 0, len(r.conns))
	for c := range r.conns {
		out = append(out, c)
	}
	return out
}

// ServerConn describes one open backend connection.
type ServerConn struct {
	// Database and User are the identity this connection authenticated as.
	// Empty until its startup exchange completes.
	Database string
	User     string

	// State is what the connection is doing: "active" while a client holds it,
	// "idle" in the pool, "login" before its startup exchange has completed,
	// and "close_needed" once its socket has failed.
	State string

	// LocalAddr and RemoteAddr are the two ends of the socket.
	LocalAddr  string
	RemoteAddr string

	ConnectedAt time.Time
	LastUsed    time.Time
	UseCount    int64
}

// ServerConns returns one row per open connection to this backend.
//
// Introspection rather than routing, so it is not on the Backend interface —
// that interface is already far past the size the guidelines allow. Callers
// assert for it, as they do for PoolStats.
func (p *Server) ServerConns() []ServerConn {
	if p == nil {
		return nil
	}

	live := p.conns.list()
	out := make([]ServerConn, 0, len(live))
	for _, c := range live {
		out = append(out, c.describe())
	}
	return out
}

// describe reads one connection's state.
func (c *Conn) describe() ServerConn {
	user, database := c.Identity()

	sc := ServerConn{
		Database:    database,
		User:        user,
		State:       c.state(),
		ConnectedAt: c.createdAt,
		LastUsed:    time.Unix(0, c.lastUsed.Load()),
		UseCount:    c.useCount.Load(),
	}
	// Nil-checked because a Conn outlives its socket: the registry drops it in
	// the driver's Close, and a caller reading the list at that moment can hold
	// one whose socket has already gone.
	if c.Conn != nil {
		if local := c.LocalAddr(); local != nil {
			sc.LocalAddr = local.String()
		}
		if remote := c.RemoteAddr(); remote != nil {
			sc.RemoteAddr = remote.String()
		}
	}
	return sc
}

// state names what this connection is doing.
//
// Ordered by what an operator most needs to know. A broken socket is reported
// even while a client still holds it, because that is the connection about to
// fail a query rather than one quietly waiting to be reaped.
func (c *Conn) state() string {
	switch {
	case c.broken.Load():
		return "close_needed"
	case !c.ready.Load():
		return "login"
	case c.busy.Load():
		return "active"
	default:
		return "idle"
	}
}

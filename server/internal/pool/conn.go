package pool

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/gpool/pkg/pooling"
)

// Conn is a backend connection managed by the pooling engine.
//
// It is the engine's type parameter (`pooling.Core[*Conn]`) rather than a bare
// net.Conn so the pool can keep its own per-connection bookkeeping — age, use
// count, and whether the socket has already failed — without a side table keyed
// by connection.
type Conn struct {
	net.Conn
	createdAt time.Time
	lastUsed  atomic.Int64 // unix nanos
	useCount  atomic.Int64
	broken    atomic.Bool
	dirty     atomic.Bool

	// stmts is the set of prepared statements the server has parsed on this
	// connection. Guarded because Recyclable may clear it from the engine's
	// maintenance goroutine while the owner is still finishing up.
	stmtMu sync.RWMutex
	stmts  map[string]struct{}

	// handle is the checked-out handle for the current acquisition. It is stored
	// by value and exactly once, per pooling.Handle's contract: a second copy
	// would carry its own release flag and could return the connection twice.
	// Only the goroutine that owns the checkout touches it, between Acquire and
	// Release, so it needs no synchronisation of its own.
	handle pooling.Handle[*Conn]
}

func NewConn(conn net.Conn) *Conn {
	c := &Conn{Conn: conn, createdAt: time.Now()}
	c.lastUsed.Store(c.createdAt.UnixNano())
	return c
}

func (c *Conn) CreatedAt() time.Time { return c.createdAt }

func (c *Conn) LastUsed() time.Time { return time.Unix(0, c.lastUsed.Load()) }

func (c *Conn) UseCount() int64 { return c.useCount.Load() }

func (c *Conn) IncUseCount() {
	c.useCount.Add(1)
	c.lastUsed.Store(time.Now().UnixNano())
}

// Read records a transport failure so the engine can discard this connection
// instead of handing a dead socket to the next caller. Driver.Dead may not do
// I/O, so the only way it can answer truthfully is if the failure was recorded
// when it happened.
func (c *Conn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		c.broken.Store(true)
	}
	return n, err
}

func (c *Conn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if err != nil {
		c.broken.Store(true)
	}
	return n, err
}

// Broken reports whether this connection has already failed a read or write.
func (c *Conn) Broken() bool { return c.broken.Load() }

// MarkBroken forces the connection to be destroyed on release.
func (c *Conn) MarkBroken() { c.broken.Store(true) }

// Dirty reports whether the connection carries state the next caller must not
// observe — an open transaction, most importantly.
func (c *Conn) Dirty() bool { return c.dirty.Load() }

// MarkDirty records that this connection is being returned with server-side
// state on it, so the engine cleans it before reuse. The gateway releases only
// at a transaction boundary, so the normal path never sets this; it exists for
// the error paths that release without knowing the transaction state.
func (c *Conn) MarkDirty() { c.dirty.Store(true) }

func (c *Conn) markClean() { c.dirty.Store(false) }

// HasStatement reports whether this connection already carries a prepared
// statement by that name, implementing protocol.StatementHolder.
//
// This is per-connection state on purpose: the session knows what the client
// asked for, but only the connection knows what the server actually parsed.
// Replaying a statement onto a connection that already has it fails with
// SQLSTATE 42P05 and leaves the session unusable.
func (c *Conn) HasStatement(name string) bool {
	c.stmtMu.RLock()
	defer c.stmtMu.RUnlock()
	_, ok := c.stmts[name]
	return ok
}

// AddStatement records that this connection now carries name.
func (c *Conn) AddStatement(name string) {
	if name == "" {
		return // the unnamed statement is replaced on every Parse, never accumulated
	}

	c.stmtMu.Lock()
	defer c.stmtMu.Unlock()
	if c.stmts == nil {
		c.stmts = make(map[string]struct{})
	}
	c.stmts[name] = struct{}{}
}

package pool

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

// connDriver is the vendor half of the pooling engine: how to dial a backend
// socket, how to judge one without doing I/O, and how to return one to a clean
// state. Everything else — capacity, idle buckets, the reaper, statistics —
// belongs to pooling.Core.
type connDriver struct {
	address     string
	dialTimeout time.Duration
	tlsConfig   *tls.Config
	handler     protocol.Handler
}

// Connect establishes one new backend connection.
func (d *connDriver) Connect(ctx context.Context) (*Conn, error) {
	dialer := net.Dialer{Timeout: d.dialTimeout}

	if d.tlsConfig != nil {
		conn, err := tls.DialWithDialer(&dialer, "tcp", d.address, d.tlsConfig)
		if err != nil {
			return nil, err
		}
		return NewConn(conn), nil
	}

	conn, err := dialer.DialContext(ctx, "tcp", d.address)
	if err != nil {
		return nil, err
	}
	return NewConn(conn), nil
}

// Close terminates a connection. It must tolerate one that is already broken.
func (d *connDriver) Close(_ context.Context, conn *Conn) error {
	if conn == nil || conn.Conn == nil {
		return nil
	}
	return conn.Conn.Close()
}

// Dead reports whether the socket has already failed. No I/O: Conn records
// transport errors as they happen precisely so this can answer from state.
func (d *connDriver) Dead(conn *Conn) bool {
	return conn == nil || conn.Conn == nil || conn.Broken()
}

// Ready implements pooling.ReadinessChecker.
//
// The pool dials a raw socket; the PostgreSQL startup exchange is the client's
// own packet, forwarded by the gateway during the handshake. A connection
// released before that completed cannot answer a query, so the engine destroys
// it rather than returning it to the idle set where a health check would pick
// it up and condemn the backend.
func (d *connDriver) Ready(conn *Conn) bool { return conn.Ready() }

// NeedsCleanup keeps the common release allocation-free. A connection returned
// at a transaction boundary — which is the normal path, since the gateway only
// releases when TxState is idle — pays nothing.
func (d *connDriver) NeedsCleanup(conn *Conn) bool {
	return conn.Dirty()
}

// Recyclable returns the connection to a state the next caller can safely
// inherit. Reached only when NeedsCleanup reported true, with a context carrying
// CleanupTimeout.
func (d *connDriver) Recyclable(ctx context.Context, conn *Conn) bool {
	if d.Dead(conn) {
		return false
	}
	if d.handler == nil {
		// Nothing can be asserted about the session, so do not risk reusing it.
		return false
	}

	// Execute drives the raw socket and does not consult the context on its own,
	// so turn the cleanup deadline into a socket deadline it cannot outlive.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return false
		}
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}

	if err := d.handler.Execute(ctx, conn, "ROLLBACK"); err != nil {
		return false
	}

	conn.markClean()
	return true
}

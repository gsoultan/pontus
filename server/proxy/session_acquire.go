package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/credentials"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// ErrNoUsableConnection is returned when every connection on offer is one this
// session cannot speak on.
var ErrNoUsableConnection = errors.New("no backend connection is usable by this session")

// startupCarrier is a connection that knows whether it has completed a
// PostgreSQL startup exchange.
type startupCarrier interface {
	Ready() bool
}

// identityCarrier is a connection that knows which user and database it
// authenticated as.
type identityCarrier interface {
	BelongsTo(user, database string) bool
}

// warnedNoSplit keeps the explanation to one line per process.
var warnedNoSplit sync.Once

// acquireForSession returns a connection this session can actually speak on.
//
// Pontus never performs a startup exchange of its own: it forwards the client's
// startup packet, once, onto the single connection acquired for the handshake.
// Every other connection the pool creates is a raw socket that has never
// negotiated anything. Handing one to a session that is past its handshake does
// not fail loudly — the client simply stops getting answers and the session
// dies with "conn closed", which is what happened the moment a second backend
// was configured and reads began routing to it.
//
// So a mid-session acquisition insists on a connection that carries a startup
// exchange, and falls back to the backend that performed this session's
// handshake before giving up. The consequence is honest and visible: with no
// usable connection on a replica, reads stay on the primary rather than
// breaking. The read/write split does not work until Pontus can authenticate
// backend connections itself — see finding A8.
func (g *Gateway) acquireForSession(
	ctx context.Context,
	hint balancer2.Hint,
	home pool2.Backend,
	state *protocol.SessionState,
) (pool2.Backend, net.Conn, error) {
	user, database := state.User, state.Database

	backend, conn, err := g.acquireBackendFor(ctx, hint, user, database)
	if err == nil {
		if usable(conn) && belongsTo(conn, user, database) {
			return backend, conn, nil
		}

		// A connection that has never completed a startup exchange used to be
		// the end of the line — it could not be used and the session fell back
		// to the backend that handled its handshake, which is why reads never
		// reached a replica. Holding the session's ClientKey changes that:
		// Pontus can authenticate this connection as the same user, right here,
		// and the read goes where the balancer sent it.
		if !usable(conn) && g.canAuthenticateBackends(state) {
			if authErr := g.authenticateFreshBackend(conn, state); authErr == nil {
				return backend, conn, nil
			} else {
				slog.Warn("Could not authenticate a backend connection for this session",
					"backend", backend.Address(), "user", user, "error", authErr)
			}
		}

		// Releasing an unready connection destroys it rather than returning it
		// to the idle set, so this does not poison the pool for the next caller.
		_ = backend.Release(conn)
		g.noteSplitUnavailable(backend)
	}

	// Fall back to the backend that carried this session's handshake. Its pool
	// holds the connection this session has already spoken on.
	if home == nil {
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("%w: no connection carrying a startup exchange", ErrNoUsableConnection)
	}

	conn, herr := home.AcquireFor(ctx, user, database)
	if herr != nil {
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, herr
	}
	if !usable(conn) || !belongsTo(conn, user, database) {
		_ = home.Release(conn)
		return nil, nil, fmt.Errorf("%w on %s: Pontus cannot open a backend connection "+
			"on its own, so only connections that carried a client handshake can be used",
			ErrNoUsableConnection, home.Address())
	}
	return home, conn, nil
}

// usable reports whether a connection has completed a startup exchange.
//
// A connection that does not report either way is assumed usable: the MySQL
// path and the test doubles do not implement this, and refusing them would
// break working setups to guard a PostgreSQL-specific hazard.
func usable(conn net.Conn) bool {
	carrier, ok := conn.(startupCarrier)
	if !ok {
		return true
	}
	return carrier.Ready()
}

// canAuthenticateBackends reports whether Pontus holds what it needs to open a
// connection as this session's user.
func (g *Gateway) canAuthenticateBackends(state *protocol.SessionState) bool {
	if g.credentials == nil {
		return false
	}
	switch state.Verifier.Method {
	case credentials.MethodSCRAM:
		// SCRAM needs the key recovered from the client's own proof.
		return len(state.ClientKey) > 0
	case credentials.MethodMD5:
		// The stored verifier answers a backend challenge on its own.
		return state.Verifier.MD5 != ""
	default:
		return false
	}
}

// authenticateFreshBackend performs a startup exchange on a newly dialled
// connection using the session's recovered credential.
//
// The client is not involved: it finished its own startup long ago. All that is
// needed is for this socket to reach the point where it can carry queries, and
// to be marked with the identity it authenticated as so a later acquisition
// knows whose it is.
func (g *Gateway) authenticateFreshBackend(server net.Conn, state *protocol.SessionState) error {
	if err := protocol.AuthenticateBackend(server, state.User, state.Database,
		state.ClientKey, state.Verifier, nil); err != nil {
		return err
	}

	startup, err := protocol.WaitForReady(server)
	if err != nil {
		return err
	}

	if carrier, ok := server.(interface {
		SetStartup(*protocol.Startup)
		SetIdentity(user, database string)
		MarkReady()
	}); ok {
		carrier.SetStartup(startup)
		carrier.SetIdentity(state.User, state.Database)
		carrier.MarkReady()
	}
	return nil
}

// belongsTo reports whether a connection may serve this user and database.
//
// A connection carries the credentials it authenticated with and cannot
// renegotiate them, so handing one authenticated as alice to a session for bob
// runs bob's queries with alice's privileges. Nothing today produces that —
// every client currently gets a fresh connection — but this is the check that
// has to exist before connections are shared, or the first day of real pooling
// is the first day of a cross-user data path.
//
// A connection that cannot answer is assumed to belong to the caller: the MySQL
// path and the test doubles do not implement it.
func belongsTo(conn net.Conn, user, database string) bool {
	carrier, ok := conn.(identityCarrier)
	if !ok {
		return true
	}
	return carrier.BelongsTo(user, database)
}

// noteSplitUnavailable explains the consequence once, rather than leaving an
// operator to wonder why a healthy replica receives no reads.
func (g *Gateway) noteSplitUnavailable(backend pool2.Backend) {
	if g.credentials != nil {
		return // Pontus can open its own connections; the split is available.
	}
	warnedNoSplit.Do(func() {
		slog.Warn("Read/write splitting is not in effect: a session can only use a backend "+
			"connection that carried its own handshake, and Pontus cannot open one itself. "+
			"Reads will stay on the backend that handled the handshake",
			"attempted", backend.Address(),
			"finding", "A8",
			"fix", "Pontus-side backend authentication (auth_query)")
	})
}

// releaseToPool returns a connection, marking it for reset before reuse.
//
// Every release is a potential handoff to a different client, so every release
// resets. In transaction pooling the connection goes back between transactions;
// in session pooling it only goes back when the client leaves. Either way, the
// next borrower must not inherit prepared statements, SET variables or temp
// tables.
//
// The consequence is worth stating because it surprises people: under
// transaction pooling a client's own SET does **not** survive its next
// transaction boundary. That is the documented semantics of transaction pooling
// — pgbouncer behaves identically — and a session that needs SET to persist
// wants session pooling. An earlier attempt at this reset was reverted after
// reading that behaviour as a regression; it was not.
func releaseToPool(backend pool2.Backend, server net.Conn) {
	if backend == nil || server == nil {
		return
	}
	if carrier, ok := server.(interface{ MarkDirty() }); ok {
		carrier.MarkDirty()
	}
	_ = backend.Release(server)
}

// resetOnRelease reports whether a reset is worth its round trip.
//
// Under passthrough a connection is never handed to another client, so the
// reset would cost a DISCARD ALL per transaction and protect nobody.
func (g *Gateway) resetOnRelease() bool { return g.credentials != nil }

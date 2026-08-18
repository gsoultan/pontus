package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"

	observability2 "github.com/gsoultan/pontus/pkg/observability"
	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// openSession reads the client's opening packet, chooses a backend for it, and
// completes the authentication exchange.
//
// The order matters. A backend used to be acquired before the client had said a
// word, which meant Pontus chose a connection without knowing the user, the
// database, or even whether the client wanted a replication stream rather than
// a session. A replication client took a pooled connection and handed it
// straight back, and there was no identity available to select a connection by
// — which is the prerequisite for pooling by (backend, database, user) rather
// than handing whatever is idle to whoever asks.
//
// Protocols where the server speaks first — MySQL sends the greeting — cannot
// work this way: Pontus would have to invent a greeting before it has a backend
// to borrow one from. Those handlers do not implement StartupReader and keep the
// original order.
func (g *Gateway) openSession(
	ctx context.Context,
	client net.Conn,
	state *protocol.SessionState,
	remoteAddr string,
) (backendOut pool2.Backend, serverOut net.Conn, clientOut net.Conn, err error) {
	// clientOut is returned because a TLS upgrade replaces the connection.
	clientOut = client
	reader, identityFirst := g.handler.(protocol.StartupReader)

	var req *protocol.StartupRequest
	if identityFirst {
		// No backend is held here, so a replication request costs nothing to
		// discover and a client that disconnects mid-startup takes nothing
		// with it.
		req, err = reader.ReadStartup(client, state)
		if err != nil {
			return nil, nil, clientOut, err
		}

		// A client that negotiated TLS is now on an encrypted connection, and
		// the plaintext one it opened with cannot be used again. Everything
		// after this — authentication, the startup completion, and the whole
		// session loop — has to go through the upgraded connection.
		if req.Conn != nil {
			client = req.Conn
			clientOut = client
		}

		// A cancel request is not a session. It names a query running on
		// another connection, gets no reply, and needs no backend of its own —
		// so it is routed and the connection closed, without ever touching the
		// pool.
		if req.Cancel != nil {
			if cerr := g.routeCancel(req.Cancel); cerr != nil {
				slog.Warn("Could not deliver a cancel request",
					"client", remoteAddr, "error", cerr)
			}
			return nil, nil, clientOut, errCancelHandled
		}

		// Authenticate the client before a backend is chosen. An unauthenticated
		// client should not be able to make Pontus open a database connection at
		// all — otherwise anyone who can reach the port can consume the pool.
		if g.credentials != nil {
			if err := g.authenticateClient(ctx, client, req, state); err != nil {
				return nil, nil, clientOut, err
			}
		}
	}

	hint := balancer2.Hint{
		CallerZone: g.current().localZone,
		ReadOnly:   false,
		Key:        remoteAddr,
	}

	// Pools hold every identity together, so an idle connection belonging to a
	// different user can be handed back repeatedly. Each miss is discarded
	// rather than returned to the idle set, which guarantees progress; a bound
	// keeps a pool full of one busy user's connections from spinning here.
	//
	// This churns, and per-identity pools are what remove the churn. Correctness
	// does not wait for them: without this loop a session either fails or, worse,
	// runs on somebody else's credentials.
	const identityAttempts = 8

	var backend pool2.Backend
	var server net.Conn

	clientOut = client

	for attempt := range identityAttempts {
		backend, server, err = g.acquireBackendFor(ctx, hint, state.User, state.Database)
		if err != nil {
			slog.Error("Failed to acquire backend for handshake", "client", remoteAddr, "error", err)
			return nil, nil, clientOut, err
		}

		switch {
		case identityFirst && g.credentials != nil && req != nil:
			// Pontus authenticated the client itself, so the backend gets its own
			// exchange rather than a forwarded packet.
			err = g.openAuthenticatedBackend(client, server, req, state)
		case identityFirst:
			err = reader.CompleteHandshake(ctx, client, server, req, state)
		default:
			err = g.handler.Handshake(ctx, client, server, state)
		}
		if err == nil {
			break
		}

		if errors.Is(err, ErrWrongIdentity) {
			// Destroy it rather than release it: returning another user's
			// connection to the idle set is how the next attempt draws the same
			// one and the loop makes no progress.
			if broken, ok := server.(interface{ MarkBroken() }); ok {
				broken.MarkBroken()
			}
			_ = backend.Release(server)
			observability2.IdentityMismatches.WithLabelValues(backend.Address()).Inc()
			if attempt == identityAttempts-1 {
				slog.Warn("Gave up finding a connection for this session's identity",
					"client", remoteAddr, "user", state.User, "attempts", identityAttempts)
				return nil, nil, clientOut, err
			}
			continue
		}

		// This connection never completed a startup exchange, so it was never
		// marked ready and the pool destroys it on release rather than handing
		// an unusable socket to the next caller.
		_ = backend.Release(server)
		return nil, nil, clientOut, err
	}

	// The startup exchange completed, so this connection can carry queries and
	// may be recycled. Recording the identity it was authenticated for is what
	// lets a later acquisition tell whose connection this is.
	if c, ok := server.(interface{ MarkReady() }); ok {
		c.MarkReady()
	}
	if c, ok := server.(interface{ SetIdentity(user, database string) }); ok {
		c.SetIdentity(state.User, state.Database)
	}

	return backend, server, clientOut, nil
}

// errCancelHandled reports that the connection carried a cancel request, which
// has been routed. Not a failure: the connection is simply finished.
var errCancelHandled = errors.New("cancel request handled")

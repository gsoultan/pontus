package proxy

import (
	"context"
	"log/slog"
	"net"

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
) (pool2.Backend, net.Conn, error) {
	reader, identityFirst := g.handler.(protocol.StartupReader)

	var req *protocol.StartupRequest
	if identityFirst {
		var err error
		// No backend is held here, so a replication request costs nothing to
		// discover and a client that disconnects mid-startup takes nothing
		// with it.
		req, err = reader.ReadStartup(client, state)
		if err != nil {
			return nil, nil, err
		}

		// Authenticate the client before a backend is chosen. An unauthenticated
		// client should not be able to make Pontus open a database connection at
		// all — otherwise anyone who can reach the port can consume the pool.
		if g.credentials != nil {
			if err := g.authenticateClient(ctx, client, req, state); err != nil {
				return nil, nil, err
			}
		}
	}

	backend, server, err := g.acquireBackend(ctx, balancer2.Hint{
		CallerZone: g.current().localZone,
		ReadOnly:   false,
		Key:        remoteAddr,
	})
	if err != nil {
		slog.Error("Failed to acquire backend for handshake", "client", remoteAddr, "error", err)
		return nil, nil, err
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
	if err != nil {
		// This connection never completed a startup exchange, so it was never
		// marked ready and the pool destroys it on release rather than handing
		// an unusable socket to the next caller.
		backend.Release(server)
		return nil, nil, err
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

	return backend, server, nil
}

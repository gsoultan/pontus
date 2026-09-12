package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/gsoultan/pontus/pkg/config"
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

		// The administration console is a database with no backend behind it.
		// Checked after authentication and before acquisition, because a
		// console session must prove who it is like any other, and must not
		// take a connection from a pool it will never use.
		if admin := g.current().admin; admin.handles(req.Database) {
			return nil, nil, clientOut, admin.serve(client, req.User)
		}

		// Resolve the client-visible database name to the one Pontus will
		// actually open, before anything downstream sees it.
		//
		// Done exactly once, here: the pool is keyed by database, the backend
		// startup names it, and the connection records it for the identity
		// check on reuse. If those three could disagree about which database a
		// session is on, a connection would be filed under one name and opened
		// against another — which is the shape of finding A11.
		if err := resolveDatabase(g.current().routes, req, state); err != nil {
			return nil, nil, clientOut, err
		}
	}

	// A primary if there is one, a replica rather than nothing.
	//
	// This connection carries the client's authentication, not its first
	// statement — nothing is known yet about what the session will ask for, so
	// it is hinted toward a primary to keep the session able to write. But
	// insisting on one meant that while the primary was down no session could
	// be opened at all, read-only or otherwise: a deployment kept replicas
	// exactly for that outage and then could not open a session to use them.
	//
	// Settling for a replica cannot cost a session anything it would otherwise
	// have had. The alternative on this path is not a primary, it is no
	// connection.
	hint := balancer2.Hint{
		CallerZone:    g.current().localZone,
		ReadOnly:      false,
		AcceptReplica: true,
		Key:           remoteAddr,
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

		// Two of these three branches perform the startup exchange by writing
		// the client's own packet to the backend. Only the first can use a
		// connection that has already completed one.
		relaysStartupPacket := !(identityFirst && g.credentials != nil && req != nil)

		switch {
		case relaysStartupPacket && alreadyStarted(server):
			// A StartupMessage has no type byte — it opens with a four-byte
			// length. Sent down a connection that is past its startup, the
			// backend reads that length's first byte as a message type and
			// answers `invalid frontend message type 0`, which kills the
			// connection and takes the client's session with it.
			//
			// The pool has no notion of "fresh", so under concurrent connects
			// a handshake could draw a connection some finished session had
			// released. Sequentially it never did, which is why every existing
			// test passed: it takes two sessions arriving at once.
			err = errNeedsFreshConnection
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

		if errors.Is(err, ErrWrongIdentity) || errors.Is(err, errNeedsFreshConnection) {
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

// resolveDatabase applies the `databases:` routing table to a startup request.
//
// Both the parsed field and the raw packet are updated. The raw packet is what
// passthrough forwards to the backend, so leaving it alone would mean the pool
// was keyed by the alias while the connection opened the name the client sent.
func resolveDatabase(routes config.Databases, req *protocol.StartupRequest, state *protocol.SessionState) error {
	if len(routes) == 0 {
		return nil
	}

	route := routes.Resolve(req.Database)
	if route.Database == req.Database {
		return nil
	}

	rewritten, err := protocol.RewriteStartupDatabase(req.Raw, route.Database)
	if err != nil {
		return fmt.Errorf("routing database %q to %q: %w", req.Database, route.Database, err)
	}

	slog.Debug("Routed a client database name",
		"requested", req.Database, "resolved", route.Database, "user", req.User)

	req.Raw = rewritten
	req.Database = route.Database
	state.Database = route.Database
	return nil
}

// errCancelHandled reports that the connection carried a cancel request, which
// has been routed. Not a failure: the connection is simply finished.
var errCancelHandled = errors.New("cancel request handled")

// errNeedsFreshConnection reports that a connection cannot carry a startup
// exchange because it has already carried one.
//
// Retried like an identity mismatch: the connection goes back destroyed rather
// than idle, so the next attempt cannot draw the same one, and the loop makes
// progress.
var errNeedsFreshConnection = errors.New("connection has already completed a startup exchange")

// alreadyStarted reports whether a pooled connection has completed a startup
// exchange. A connection that cannot say is assumed fresh, which is what a
// plain net.Conn from a test double is.
func alreadyStarted(server net.Conn) bool {
	carrier, ok := server.(interface{ Ready() bool })
	return ok && carrier.Ready()
}

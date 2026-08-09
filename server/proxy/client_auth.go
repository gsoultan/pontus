package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/gsoultan/pontus/server/internal/credentials"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// authenticateClient verifies the client and keeps the credential its backend
// connections will need.
//
// Deliberately before a backend is acquired. An unauthenticated client must not
// be able to make Pontus open a database connection, or anyone who can reach
// the port can exhaust the pool without ever proving who they are.
func (g *Gateway) authenticateClient(
	ctx context.Context,
	client net.Conn,
	req *protocol.StartupRequest,
	state *protocol.SessionState,
) error {
	verifier, err := g.credentials.Lookup(ctx, req.User)
	if err != nil {
		// An unknown user and a wrong password are reported identically. Telling
		// them apart hands anyone who can open a socket a list of real accounts.
		slog.Debug("Credential lookup failed", "user", req.User, "error", err)
		_ = protocol.WriteClientError(client, "28P01", protocol.ErrAuthRejected.Error())
		return protocol.ErrAuthRejected
	}

	clientKey, err := protocol.AuthenticateClient(client, req.User, verifier)
	if err != nil {
		if errors.Is(err, protocol.ErrUnsupportedAuth) {
			// A configuration problem rather than a bad password: say so, because
			// "authentication failed" would send an operator hunting a password
			// that is perfectly correct.
			slog.Error("Cannot authenticate this role", "user", req.User, "error", err)
			_ = protocol.WriteClientError(client, "0A000", err.Error())
			return err
		}
		_ = protocol.WriteClientError(client, "28P01", protocol.ErrAuthRejected.Error())
		return protocol.ErrAuthRejected
	}

	// Password-equivalent for this session. Held only for its lifetime.
	state.ClientKey = clientKey
	state.Verifier = verifier.SCRAM
	return nil
}

// openAuthenticatedBackend performs Pontus's own startup exchange on a freshly
// acquired backend connection, then finishes the client's startup with what the
// backend reported.
func (g *Gateway) openAuthenticatedBackend(
	client net.Conn,
	server net.Conn,
	req *protocol.StartupRequest,
	state *protocol.SessionState,
) error {
	// A connection that has already completed a startup exchange must not be
	// given another: the backend is past that phase and would read a
	// StartupMessage as a malformed command. Acquisition has already guaranteed
	// it belongs to this user and database, so it is ready to carry queries as
	// it stands — this is the case that makes pooling worth anything.
	if carrier, ok := server.(interface {
		Ready() bool
		Startup() *protocol.Startup
	}); ok && carrier.Ready() {
		return protocol.CompleteClientStartup(client, carrier.Startup())
	}

	if err := protocol.AuthenticateBackend(server, req.User, req.Database,
		state.ClientKey, state.Verifier, nil); err != nil {
		return err
	}

	startup, err := protocol.WaitForReady(server)
	if err != nil {
		return err
	}

	// Keep it for whoever borrows this connection next.
	if carrier, ok := server.(interface{ SetStartup(*protocol.Startup) }); ok {
		carrier.SetStartup(startup)
	}

	// The client is waiting for the parameters, the backend key and the
	// ReadyForQuery that a relayed handshake would have delivered.
	return protocol.CompleteClientStartup(client, startup)
}

// SetCredentialStore switches Pontus into authenticating clients itself.
//
// Nil keeps passthrough, which is the default and must keep working with no
// auth block configured at all.
func (g *Gateway) SetCredentialStore(store credentials.Store) {
	g.credentials = store
}

package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/gsoultan/pontus/server/internal/credentials"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// ErrWrongIdentity marks a connection that belongs to a different user or
// database than the session asking for it.
//
// Never reported to the client as such: it is an internal routing fault, and
// the session retries on another connection.
var ErrWrongIdentity = errors.New("connection belongs to another identity")

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
	// StartupMessage as a malformed command. Reusing it as it stands is what
	// makes pooling worth anything.
	//
	// But only for the identity it authenticated as. This check was missing when
	// reuse was introduced, and the result was not a performance problem: a
	// session was handed a connection belonging to another user and every one of
	// its queries ran with that user's privileges. `SELECT current_user`
	// returned the wrong name. Acquisition is expected to have filtered already;
	// this refuses to depend on that, because the cost of being wrong here is
	// cross-user data access.
	if carrier, ok := server.(interface {
		Ready() bool
		Startup() *protocol.Startup
		BelongsTo(user, database string) bool
	}); ok && carrier.Ready() {
		if !carrier.BelongsTo(req.User, req.Database) {
			return fmt.Errorf("%w: connection is authenticated for another identity",
				ErrWrongIdentity)
		}
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

package protocol

import (
	"errors"
	"fmt"
	"net"

	"github.com/gsoultan/pontus/server/internal/credentials"
)

// ErrAuthRejected is what a client is told when authentication fails.
//
// One error for every cause. Distinguishing "no such user" from "wrong
// password" tells anyone who can open a socket which user names are real.
var ErrAuthRejected = errors.New("password authentication failed")

// AuthenticateClient runs the client-facing half of authentication and returns
// the ClientKey needed to open backend connections for this user.
//
// This replaces relaying the client's exchange to a backend. Relaying meant the
// exchange could only ever happen once, on one connection, which is why a
// session could not be moved and connections could not be shared. Doing it here
// costs nothing extra on the wire — the same messages are exchanged — and
// yields the one thing relaying cannot: a credential Pontus can reuse.
func AuthenticateClient(client net.Conn, user string, verifier credentials.Verifier) ([]byte, error) {
	switch verifier.Method {
	case credentials.MethodSCRAM:
		return authenticateSCRAM(client, verifier.SCRAM)

	case credentials.MethodMD5:
		// An md5 verifier can answer an md5 challenge directly, but that is a
		// separate exchange in both directions and is not implemented yet.
		// Refusing is correct until it is: falling back to SCRAM here would ask
		// the client for a proof no stored md5 credential can verify.
		return nil, fmt.Errorf("%w: role %q stores an md5 verifier, which this "+
			"build cannot verify", ErrUnsupportedAuth, user)

	case credentials.MethodNone:
		// A role with no password can only arrive over a trust or peer line.
		// Pontus has no way to know pg_hba says that, so it must not decide the
		// client is authenticated on its own.
		return nil, fmt.Errorf("%w: role %q has no password", ErrAuthRejected, user)

	default:
		return nil, fmt.Errorf("%w: role %q", ErrUnsupportedAuth, user)
	}
}

// authenticateSCRAM carries the SASL exchange with a client.
func authenticateSCRAM(client net.Conn, verifier *credentials.SCRAMVerifier) ([]byte, error) {
	server, err := credentials.NewScramServer(verifier)
	if err != nil {
		return nil, err
	}

	if err := WriteAuthSASL(client); err != nil {
		return nil, err
	}

	mechanism, clientFirst, err := ReadSASLResponse(client, true)
	if err != nil {
		return nil, err
	}
	// A client that insists on channel binding is refused rather than downgraded.
	// Only SCRAM-SHA-256 was offered, so anything else is a client ignoring the
	// offer.
	if mechanism != saslMechanismSCRAM {
		return nil, fmt.Errorf("%w: client chose %q, which was not offered",
			ErrUnsupportedAuth, mechanism)
	}

	serverFirst, err := server.Begin(string(clientFirst))
	if err != nil {
		return nil, err
	}
	if err := WriteAuthSASLContinue(client, serverFirst); err != nil {
		return nil, err
	}

	_, clientFinal, err := ReadSASLResponse(client, false)
	if err != nil {
		return nil, err
	}

	serverFinal, err := server.Finish(string(clientFinal))
	if err != nil {
		// Every failure reaches the client as the same message.
		return nil, fmt.Errorf("%w", ErrAuthRejected)
	}

	if err := WriteAuthSASLFinal(client, serverFinal); err != nil {
		return nil, err
	}
	if err := WriteAuthOK(client); err != nil {
		return nil, err
	}

	return server.ClientKey(), nil
}

// CompleteClientStartup finishes a client's startup after authentication, using
// the parameters the backend reported.
//
// A client expects ParameterStatus for things like server_version and
// client_encoding, then ReadyForQuery. Those normally arrive from the backend
// during a relayed handshake; when Pontus authenticates the client itself it
// has to supply them from the backend connection it opened.
func CompleteClientStartup(client net.Conn, startup *Startup) error {
	if startup == nil {
		return fmt.Errorf("no startup information to complete the client with")
	}

	for key, value := range startup.Params {
		body := make([]byte, 0, len(key)+len(value)+2)
		body = append(body, key...)
		body = append(body, 0)
		body = append(body, value...)
		body = append(body, 0)
		if err := writeTagged(client, 'S', body); err != nil {
			return err
		}
	}

	// BackendKeyData. PostgreSQL always sends it, and a client that models the
	// startup sequence strictly — asyncpg does — treats its absence as a
	// protocol error rather than a missing feature. Omitting it made asyncpg
	// fail with "protocol.data_received() call failed" while pgx and libpq
	// carried on, which is the whole argument for testing more than one driver.
	if len(startup.BackendKey) > 0 {
		if err := writeTagged(client, 'K', startup.BackendKey); err != nil {
			return err
		}
	}

	// ReadyForQuery with transaction status 'I' (idle).
	return writeTagged(client, 'Z', []byte{'I'})
}

// WriteClientError reports a startup failure to a client in the form its driver
// expects, so it prints a reason instead of "connection closed".
//
// Severity FATAL and no ReadyForQuery: the client has not been authenticated,
// so it is not in the query phase and telling it otherwise describes a state
// the connection is not in.
func WriteClientError(client net.Conn, code, message string) error {
	return WriteStartupError(client, code, message)
}

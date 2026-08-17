package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/gsoultan/pontus/server/internal/credentials"
)

// AuthenticateBackend performs a PostgreSQL startup exchange as a given user,
// using a ClientKey rather than a password.
//
// This is what makes Pontus a pooler rather than a relay. Until now the only
// startup exchange it could produce was the client's own, forwarded once, so a
// session could never be given a second connection (finding A8) and connections
// could never be shared. With a ClientKey recovered from the client's own SCRAM
// proof, Pontus can open as many connections as it needs for that user without
// ever holding their password.
//
// The verifier supplies ServerKey so the backend is authenticated in return:
// skipping that would let anything answering on the backend's address complete
// the exchange.
func AuthenticateBackend(
	conn net.Conn,
	user, database string,
	clientKey []byte,
	verifier credentials.Verifier,
	params map[string]string,
) error {
	if err := writeStartup(conn, user, database, params); err != nil {
		return fmt.Errorf("startup: %w", err)
	}

	for {
		req, err := ReadAuthRequest(conn)
		if err != nil {
			return err
		}

		switch req.Type {
		case authOK:
			return nil

		case authSASL:
			if verifier.Method != credentials.MethodSCRAM {
				return fmt.Errorf("%w: backend asked for SCRAM but the stored "+
					"credential is %s", ErrUnsupportedAuth, verifier.Method)
			}
			scram, err := credentials.NewScramClient(user, clientKey)
			if err != nil {
				return err
			}
			if err := runSASL(conn, scram, req, verifier.SCRAM); err != nil {
				return err
			}
			// The server sends AuthenticationOk after SASLFinal, so the loop
			// continues rather than returning here.

		case authCleartextPassword:
			// A ClientKey cannot answer a cleartext challenge — that needs the
			// password itself, which is precisely what Pontus does not have.
			return fmt.Errorf("%w: backend asked for a cleartext password, which "+
				"cannot be answered from a SCRAM credential", ErrUnsupportedAuth)

		case authMD5Password:
			// An md5 verifier is exactly what the client proves knowledge of, so
			// it answers this challenge directly — no key recovery needed.
			if verifier.Method != credentials.MethodMD5 {
				return fmt.Errorf("%w: backend asked for md5 but the stored "+
					"credential is %s; the two are not interchangeable",
					ErrUnsupportedAuth, verifier.Method)
			}
			if err := WritePasswordMessage(conn,
				credentials.MD5Response(verifier.MD5, req.Salt)); err != nil {
				return err
			}

		default:
			return fmt.Errorf("%w: authentication type %d", ErrUnsupportedAuth, req.Type)
		}
	}
}

// runSASL carries one SCRAM exchange with a backend.
func runSASL(
	conn net.Conn,
	scram *credentials.ScramClient,
	offer *AuthRequest,
	verifier *credentials.SCRAMVerifier,
) error {
	// Refuse to proceed if the server only offers channel binding: Pontus cannot
	// bind to a channel it did not establish, and there is no safe fallback.
	if !slicesContains(offer.Mechanisms, saslMechanismSCRAM) {
		return fmt.Errorf("%w: backend offers %v, none of which Pontus can use",
			ErrUnsupportedAuth, offer.Mechanisms)
	}

	clientFirst, err := scram.First()
	if err != nil {
		return err
	}
	if err := WriteSASLInitialResponse(conn, saslMechanismSCRAM, clientFirst); err != nil {
		return err
	}

	challenge, err := ReadAuthRequest(conn)
	if err != nil {
		return err
	}
	if challenge.Type != authSASLContinue {
		return fmt.Errorf("expected a SASL challenge, got authentication type %d", challenge.Type)
	}

	clientFinal, err := scram.Final(string(challenge.Data))
	if err != nil {
		return err
	}
	if err := WriteSASLResponse(conn, clientFinal); err != nil {
		return err
	}

	final, err := ReadAuthRequest(conn)
	if err != nil {
		return err
	}
	if final.Type != authSASLFinal {
		return fmt.Errorf("expected the SASL final message, got authentication type %d", final.Type)
	}

	// Authenticate the server to us, not just us to it.
	return scram.VerifyServer(string(final.Data), verifier.ServerKey)
}

// writeStartup sends a StartupMessage.
func writeStartup(w io.Writer, user, database string, params map[string]string) error {
	body := binary.BigEndian.AppendUint32(nil, 196608) // protocol 3.0

	appendParam := func(key, value string) {
		body = append(body, key...)
		body = append(body, 0)
		body = append(body, value...)
		body = append(body, 0)
	}

	appendParam("user", user)
	if database != "" {
		appendParam("database", database)
	}
	for key, value := range params {
		// user and database are set above; a duplicate would be ambiguous.
		if key == "user" || key == "database" {
			continue
		}
		appendParam(key, value)
	}
	body = append(body, 0)

	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))
	_, err := w.Write(append(out, body...))
	return err
}

// Startup is what a backend reported between AuthenticationOk and
// ReadyForQuery.
type Startup struct {
	// Params are the ParameterStatus values: server_version, client_encoding
	// and the rest, which a client expects during its own startup.
	Params map[string]string

	// BackendKey is the raw BackendKeyData payload — the process id and secret
	// a client needs in order to cancel a query.
	//
	// Kept because it is not optional in practice. PostgreSQL always sends it,
	// and a client that models the startup sequence strictly treats its absence
	// as a protocol error rather than a missing feature. asyncpg does; pgx and
	// libpq do not, which is exactly why a single driver proves nothing.
	BackendKey []byte
}

// WaitForReady consumes the messages between AuthenticationOk and
// ReadyForQuery.
func WaitForReady(conn net.Conn) (*Startup, error) {
	out := &Startup{Params: make(map[string]string)}

	for {
		tag, body, err := readTagged(conn)
		if err != nil {
			return nil, err
		}

		switch tag {
		case 'S': // ParameterStatus
			key, rest, ok := splitCString(body)
			if !ok {
				continue
			}
			value, _, _ := splitCString(rest)
			out.Params[key] = value
		case 'K': // BackendKeyData
			out.BackendKey = append([]byte(nil), body...)
		case 'Z': // ReadyForQuery
			return out, nil
		case 'E':
			return nil, fmt.Errorf("server refused the connection: %s", errorFields(body))
		}
	}
}

func splitCString(b []byte) (string, []byte, bool) {
	end := indexByte(b, 0)
	if end < 0 {
		return "", nil, false
	}
	return string(b[:end]), b[end+1:], true
}

func slicesContains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

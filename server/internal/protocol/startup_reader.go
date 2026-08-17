package protocol

import (
	"context"
	"net"
)

// StartupRequest is what a client asked for, read before any backend is chosen.
type StartupRequest struct {
	// Raw is the startup packet exactly as the client sent it, to be forwarded
	// to whichever backend is selected.
	Raw []byte

	User        string
	Database    string
	Replication string

	// Conn is the connection to use from here on.
	//
	// A client that asked for TLS has been upgraded, and the plaintext socket it
	// opened with can no longer be read or written — everything after the
	// negotiation, including the startup packet itself, goes through the
	// encrypted one.
	Conn net.Conn
}

// StartupReader is implemented by protocols where the **client speaks first**,
// so a session's identity is known before a backend has to be chosen.
//
// This is a real protocol difference, not a convenience. PostgreSQL's client
// opens with a StartupMessage naming the user and database. MySQL's *server*
// speaks first — it sends a greeting the client replies to — so Pontus cannot
// learn who is connecting without already holding a backend connection to
// borrow a greeting from. A handler that cannot do this simply does not
// implement the interface, and the gateway keeps the older order for it.
//
// Knowing the identity first matters for two reasons:
//
//   - A connection can be chosen *for* a user and database, which is what makes
//     pooling by identity possible instead of handing whatever is idle to
//     whoever asks.
//   - A replication client can be recognised before a pooled connection is
//     taken. It used to acquire one, discover the request was a replication
//     stream, and hand it straight back.
type StartupReader interface {
	// ReadStartup reads the client's opening packet, answering any encryption
	// negotiation that precedes it. No backend is involved.
	ReadStartup(client net.Conn, state *SessionState) (*StartupRequest, error)

	// CompleteHandshake forwards the request to the chosen backend and carries
	// the authentication exchange between the two.
	CompleteHandshake(ctx context.Context, client, server net.Conn, req *StartupRequest, state *SessionState) error
}

package protocol

import (
	"context"
	"net"
)

// SessionTracker tracks and replays session-level database configuration.
type SessionTracker interface {
	// TrackSessionState updates the session state based on the intercepted data.
	TrackSessionState(state *SessionState, data []byte)

	// ReplaySessionState applies the tracked session state to a new connection.
	ReplaySessionState(ctx context.Context, conn net.Conn, state *SessionState) error

	// TrackPreparedStatement tracks prepared statements in the session.
	TrackPreparedStatement(state *SessionState, data []byte)

	// ReplayPreparedStatements replays tracked prepared statements to a new connection.
	ReplayPreparedStatements(ctx context.Context, conn net.Conn, state *SessionState) error

	// IsPinned checks if the session has any state that requires it to stay on the same connection.
	IsPinned(state *SessionState) bool
}

// StatementHolder is implemented by a backend connection that remembers which
// prepared statements it already carries.
//
// Replay is per-connection state, not per-session state: a session's statement
// list says what the client believes exists, while only the connection knows
// what the server actually has. Replaying without consulting the connection is
// what produces "prepared statement ... already exists" (SQLSTATE 42P05) on the
// second query of a pooled session.
//
// The pool's connection type implements this; protocol reaches it by assertion
// so that this package keeps no dependency on the pool.
type StatementHolder interface {
	// HasStatement reports whether this connection already parsed name.
	HasStatement(name string) bool

	// AddStatement records that this connection now carries name.
	AddStatement(name string)
}

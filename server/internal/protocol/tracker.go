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

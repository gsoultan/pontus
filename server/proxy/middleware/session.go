package middleware

import (
	"bytes"
	"net"

	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// Session represents a client session.
type Session struct {
	Client     net.Conn
	RemoteAddr string
	State      *protocol.SessionState
	Backend    pool.Backend

	// HomeBackend is the backend that carried this session's handshake.
	//
	// It is the only pool holding a connection this session has completed a
	// startup exchange on, so it is where acquisition falls back when the
	// balancer's choice cannot serve this session. Set once and never cleared,
	// unlike Backend which is released at every transaction boundary.
	HomeBackend     pool.Backend
	Server          net.Conn
	Buffer          []byte
	Normalized      string
	QueryInfo       protocol.QueryInfo
	Data            []byte
	ShouldReplay    bool
	ResponseCapture *bytes.Buffer

	// ReplyFailed records that the backend answered with an ErrorResponse.
	//
	// Set by the gateway, which frames the reply anyway, rather than rescanned
	// here: a result set is megabytes and a second pass over it to find one
	// message type is a pass nobody needs.
	ReplyFailed bool
}

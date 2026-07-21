package middleware

import (
	"bytes"
	"net"

	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// Session represents a client session.
type Session struct {
	Client          net.Conn
	RemoteAddr      string
	State           *protocol.SessionState
	Backend         pool.Backend
	Server          net.Conn
	Buffer          []byte
	Normalized      string
	QueryInfo       protocol.QueryInfo
	Data            []byte
	ShouldReplay    bool
	ResponseCapture *bytes.Buffer
}

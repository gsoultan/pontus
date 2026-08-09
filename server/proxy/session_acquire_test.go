package proxy

import (
	"net"
	"testing"
)

// readyConn reports whether it completed a startup exchange.
type readyConn struct {
	net.Conn
	ready bool
}

func (c *readyConn) Ready() bool { return c.ready }

// plainConn cannot answer the question at all.
type plainConn struct{ net.Conn }

func TestUsableRequiresAStartupExchange(t *testing.T) {
	if usable(&readyConn{ready: false}) {
		t.Error("a connection that never completed a startup exchange was accepted; " +
			"handing one to a live session kills it silently")
	}
	if !usable(&readyConn{ready: true}) {
		t.Error("a connection that completed its startup exchange was rejected")
	}
}

// The MySQL path and the test doubles do not implement the check. Refusing them
// would break working setups to guard a PostgreSQL-specific hazard, so silence
// means yes.
func TestUsableAssumesYesWhenTheConnectionCannotSay(t *testing.T) {
	if !usable(&plainConn{}) {
		t.Error("a connection that does not report readiness was refused")
	}
}

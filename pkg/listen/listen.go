// Package listen opens the sockets Pontus serves on.
//
// It exists for one reason: a binary upgrade should not drop connections. The
// old process holds the port until it exits, so a new one cannot bind it, and
// the gap between them is an outage on the only port that matters. SO_REUSEPORT
// lets both bind at once, so the new process is serving before the old one
// stops.
package listen

import (
	"context"
	"net"
)

// Config decides how a socket is opened.
type Config struct {
	// ReusePort lets another process bind the same address.
	//
	// Off by default, and deliberately: "address already in use" is a useful
	// error. With this on, a second Pontus started by mistake — a stale unit
	// file, a duplicated deploy — does not fail. It silently takes half the
	// traffic, which is a much worse thing to debug than a refused start.
	//
	// Turn it on when you want zero-downtime upgrades, and accept that the
	// guard goes with it.
	ReusePort bool
}

// TCP opens a listening socket.
func (c Config) TCP(ctx context.Context, addr string) (net.Listener, error) {
	lc := net.ListenConfig{}
	if c.ReusePort {
		lc.Control = controlReusePort
	}
	return lc.Listen(ctx, "tcp", addr)
}

// Listen opens a socket with the default configuration.
func Listen(addr string) (net.Listener, error) {
	return Config{}.TCP(context.Background(), addr)
}

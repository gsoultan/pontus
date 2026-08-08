//go:build e2e

package e2e

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// relay forwards TCP to the real backend and can be severed on demand.
//
// Exists so a test can take the database away mid-session. Everything else in
// this suite exercises a healthy backend, but what a proxy is judged on is what
// it does when the database goes away underneath an open connection — and that
// cannot be tested against a database the rest of the suite depends on.
type relay struct {
	listener net.Listener

	mu      sync.Mutex
	conns   []net.Conn
	severed bool
}

// newRelay starts forwarding to target until the test ends.
func newRelay(t *testing.T, target string) *relay {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("relay listen: %v", err)
	}

	r := &relay{listener: listener}
	t.Cleanup(r.close)

	go r.accept(target)
	return r
}

func (r *relay) addr() string { return r.listener.Addr().String() }

func (r *relay) accept(target string) {
	for {
		client, err := r.listener.Accept()
		if err != nil {
			return
		}

		r.mu.Lock()
		severed := r.severed
		r.mu.Unlock()

		if severed {
			client.Close()
			continue
		}
		go r.forward(client, target)
	}
}

func (r *relay) forward(client net.Conn, target string) {
	server, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		client.Close()
		return
	}

	r.track(client, server)

	go func() {
		_, _ = io.Copy(server, client)
		server.Close()
	}()
	_, _ = io.Copy(client, server)
	client.Close()
}

func (r *relay) track(conns ...net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns = append(r.conns, conns...)
}

// sever drops every live connection and refuses new ones, which is what a
// database going down looks like from the proxy's side.
func (r *relay) sever() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.severed = true
	for _, c := range r.conns {
		_ = c.Close()
	}
	r.conns = nil
}

// restore resumes forwarding after a sever, standing in for the database
// coming back at the same address. The listener never closed, so there is no
// port to race for.
func (r *relay) restore() {
	r.mu.Lock()
	r.severed = false
	r.mu.Unlock()
}

func (r *relay) close() {
	r.sever()
	_ = r.listener.Close()
}

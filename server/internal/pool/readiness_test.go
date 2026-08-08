package pool

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

// A connection acquired and released without completing the PostgreSQL startup
// exchange must be destroyed, not pooled.
//
// The pool hands out a raw socket — the startup packet is the client's own and
// is forwarded by the gateway — so an abandoned acquisition leaves a socket
// that cannot answer anything. Pooled, it is indistinguishable from a ready
// connection, and the pool's own deepCheck picks it up, sends SELECT 1, gets
// nothing, and marks the whole backend unhealthy.
func TestUnreadyConnectionIsNotPooled(t *testing.T) {
	addr := listenAndHold(t)

	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, 4, 0, time.Second, h, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	conn, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Released without MarkReady: the handshake never happened.
	if err := p.Release(conn); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if got := p.Stats().IdleConns; got != 0 {
		t.Errorf("an unready connection was returned to the idle set: IdleConns = %d, want 0", got)
	}
}

// The normal path — the gateway completes the handshake — must still recycle.
func TestReadyConnectionIsPooled(t *testing.T) {
	addr := listenAndHold(t)

	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, 4, 0, time.Second, h, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	conn, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	markReadyForTest(conn)

	if err := p.Release(conn); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if got := p.Stats().IdleConns; got != 1 {
		t.Errorf("a ready connection was destroyed: IdleConns = %d, want 1", got)
	}
}

// Readiness is a property of the connection, so it survives being recycled —
// a session must not have to redo the startup exchange on every acquisition.
func TestReadinessSurvivesRecycling(t *testing.T) {
	addr := listenAndHold(t)

	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, 4, 0, time.Second, h, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	first, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	markReadyForTest(first)
	if err := p.Release(first); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer p.Release(second)

	c, ok := second.(*Conn)
	if !ok {
		t.Fatalf("expected *Conn, got %T", second)
	}
	if !c.Ready() {
		t.Error("a recycled connection came back unready; the handshake would be redone every acquisition")
	}
}

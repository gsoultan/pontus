package pool

import (
	"net"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

func TestServerPool_AcquireRelease(t *testing.T) {
	// Start a dummy server to dial to
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9091", "test-token", RolePrimary, 1, 2, 1, 1*time.Second, h, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// Acquire 1
	c1, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.ActiveConns() != 1 {
		t.Errorf("Expected 1 active conn, got %d", p.ActiveConns())
	}

	// Release 1
	p.Release(c1)
	if p.ActiveConns() != 0 {
		t.Errorf("Expected 0 active conn, got %d", p.ActiveConns())
	}

	// Acquire again (should get from idle)
	c2, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.ActiveConns() != 1 {
		t.Errorf("Expected 1 active conn, got %d", p.ActiveConns())
	}
	p.Release(c2)
}

package pool

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

func TestServerPool_IdleCleanup(t *testing.T) {
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
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, 2, 0, 1*time.Second, h, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.maxIdleTime = 100 * time.Millisecond
	p.Start(t.Context())
	defer p.Close()

	// Acquire and release a connection
	c1, _ := p.Acquire(t.Context())
	p.Release(c1)

	// Check idle connections in shards
	idleCount := 0
	for _, s := range p.shards {
		idleCount += len(s.idleConns)
	}
	if idleCount != 1 {
		t.Errorf("Expected 1 idle connection, got %d", idleCount)
	}

	// Wait for cleanup
	time.Sleep(300 * time.Millisecond)

	idleCount = 0
	for _, s := range p.shards {
		idleCount += len(s.idleConns)
	}
	if idleCount != 0 {
		t.Errorf("Expected 0 idle connection after cleanup, got %d", idleCount)
	}
}

func TestServerPool_CircuitBreaker(t *testing.T) {
	// No server listening -> dial will fail
	h := protocol.NewPostgresHandler()
	p, err := NewServer("127.0.0.1:1", "", "127.0.0.1:9093", "test-token", RolePrimary, 1, 2, 0, 100*time.Millisecond, h, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Fail 5 times to open the breaker
	for i := 0; i < 5; i++ {
		_, err := p.Acquire(context.Background())
		if err == nil {
			t.Fatal("Expected error, got nil")
		}
	}

	// Next call should fail fast due to circuit breaker
	start := time.Now()
	_, err = p.Acquire(context.Background())
	duration := time.Since(start)

	if err == nil {
		t.Fatal("Expected circuit breaker error, got nil")
	}

	// Should fail much faster than the 100ms dial timeout
	if duration > 50*time.Millisecond {
		t.Errorf("Expected fast failure from circuit breaker, took %v", duration)
	}
}

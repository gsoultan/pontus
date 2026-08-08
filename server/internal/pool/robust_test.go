package pool

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

// listenAndHold accepts connections and keeps them open until the test ends, so
// the pool sees a backend that behaves like a real server.
func listenAndHold(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	var mu sync.Mutex
	var held []net.Conn
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()

	return ln.Addr().String()
}

func TestServerPool_ReleaseReturnsConnectionToPool(t *testing.T) {
	addr := listenAndHold(t)

	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, 2, 0, time.Second, h, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	c1, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Stand in for the gateway completing the startup exchange; without it the
	// pool destroys the connection on release rather than recycling it.
	markReadyForTest(c1)
	if got := p.Stats().ActiveConns; got != 1 {
		t.Errorf("ActiveConns while checked out = %d, want 1", got)
	}

	if err := p.Release(c1); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := p.Stats().IdleConns; got != 1 {
		t.Errorf("IdleConns after release = %d, want 1", got)
	}
	if got := p.Stats().ActiveConns; got != 0 {
		t.Errorf("ActiveConns after release = %d, want 0", got)
	}

	// The pooled connection must be handed back out rather than a new one dialled.
	c2, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if c2 != c1 {
		t.Error("expected the pooled connection to be reused")
	}
	p.Release(c2)
}

// TestServerPool_MaxConnsIsHardCeiling pins the property the previous engine did
// not hold: capacity was a check-then-act against a pool-global counter under a
// per-shard lock, so concurrent acquirers could all observe room and each dial,
// overshooting max_conns. A pooler exists to bound connections to the database,
// so this is the contract that matters most.
func TestServerPool_MaxConnsIsHardCeiling(t *testing.T) {
	addr := listenAndHold(t)

	const maxConns = 4
	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, maxConns, 0, time.Second, h, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var acquired []net.Conn

	for range 64 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()

			conn, err := p.Acquire(ctx)
			if err != nil {
				return // exhausted is the correct answer, not a failure
			}
			mu.Lock()
			acquired = append(acquired, conn)
			mu.Unlock()
		})
	}
	wg.Wait()

	if len(acquired) > maxConns {
		t.Errorf("handed out %d connections with max_conns=%d", len(acquired), maxConns)
	}
	if total := p.Stats().ActiveConns; total > maxConns {
		t.Errorf("ActiveConns = %d, exceeds max_conns=%d", total, maxConns)
	}

	for _, c := range acquired {
		p.Release(c)
	}
}

func TestServerPool_CircuitBreaker(t *testing.T) {
	// No server listening -> dial will fail
	h := protocol.NewPostgresHandler()
	p, err := NewServer("127.0.0.1:1", "", "127.0.0.1:9093", "test-token", RolePrimary, 1, 2, 0, 100*time.Millisecond, h, nil, nil, "")
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

// Capacity is adjustable again now that the engine supports it. The previous
// implementation resized by mutating an AIMD counter that any error could halve;
// this asks the engine, which enforces the ceiling structurally.
func TestServerPool_SetMaxConnsBoundsCheckouts(t *testing.T) {
	addr := listenAndHold(t)

	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, 8, 0, time.Second, h, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.SetMaxConns(2); err != nil {
		t.Fatalf("SetMaxConns(2): %v", err)
	}

	var held []net.Conn
	for range 2 {
		c, err := p.Acquire(t.Context())
		if err != nil {
			t.Fatalf("Acquire within the new ceiling: %v", err)
		}
		held = append(held, c)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := p.Acquire(ctx); err == nil {
		t.Error("acquired a third connection after lowering the ceiling to 2")
	}

	for _, c := range held {
		p.Release(c)
	}

	// Above the configured max_conns is refused rather than silently clamped.
	if err := p.SetMaxConns(9); err == nil {
		t.Error("SetMaxConns above the configured max_conns should be refused")
	}
}

// A demoted primary must not hand out connections it opened while it was writable.
func TestServerPool_ClearIdleConnsDropsPooled(t *testing.T) {
	addr := listenAndHold(t)

	h := protocol.NewPostgresHandler()
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1, 4, 0, time.Second, h, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	c, err := p.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	markReadyForTest(c)
	p.Release(c)

	if got := p.Stats().IdleConns; got != 1 {
		t.Fatalf("expected 1 idle connection before eviction, got %d", got)
	}

	p.clearIdleConns()

	if got := p.Stats().IdleConns; got != 0 {
		t.Errorf("%d idle connections survived the role change", got)
	}
}

// markReadyForTest stands in for the gateway completing the PostgreSQL startup
// exchange. The pool only recycles a connection that reached that point, so a
// test asserting on the idle set has to reach it too.
func markReadyForTest(conn net.Conn) {
	if c, ok := conn.(*Conn); ok {
		c.MarkReady()
	}
}

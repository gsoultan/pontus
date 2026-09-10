package listen

import (
	"context"
	"net"
	"runtime"
	"testing"
)

// Without the option a second bind fails, which is the guard an operator relies
// on to notice a duplicated deploy.
func TestSecondBindFailsWithoutReusePort(t *testing.T) {
	first, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := Listen(first.Addr().String())
	if err == nil {
		_ = second.Close()
		t.Error("a second process could bind the same address without reuse_port")
	}
}

// With it, two live sockets share the address — which is what carries traffic
// across a binary upgrade.
func TestReusePortAllowsASecondBind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SO_REUSEPORT is a unix facility")
	}

	cfg := Config{ReusePort: true}
	ctx := context.Background()

	first, err := cfg.TCP(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := cfg.TCP(ctx, first.Addr().String())
	if err != nil {
		t.Fatalf("second listen with reuse_port: %v", err)
	}
	defer func() { _ = second.Close() }()

	if first.Addr().String() != second.Addr().String() {
		t.Errorf("the two listeners are on %s and %s, want the same address",
			first.Addr(), second.Addr())
	}
}

// The property an upgrade depends on: while both sockets are open, every
// connection is served and none is refused.
//
// Deliberately not asserting a distribution. Linux (3.9+) balances new
// connections across the sockets; Darwin and the BSDs hand them all to the most
// recent binder. Both are fine for an upgrade — there is no gap either way —
// and a test that pinned one behaviour would fail on the other platform for no
// reason that matters.
func TestNoConnectionIsRefusedWhileBothListen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SO_REUSEPORT is a unix facility")
	}

	cfg := Config{ReusePort: true}
	ctx := context.Background()

	first, err := cfg.TCP(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer func() { _ = first.Close() }()

	addr := first.Addr().String()
	second, err := cfg.TCP(ctx, addr)
	if err != nil {
		t.Fatalf("second listen: %v", err)
	}
	defer func() { _ = second.Close() }()

	served := make(chan struct{}, 64)
	for _, ln := range []net.Listener{first, second} {
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				_ = conn.Close()
				served <- struct{}{}
			}
		}()
	}

	const attempts = 40
	for i := range attempts {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("connection %d was refused while both sockets were open: %v", i, err)
		}
		_ = conn.Close()
	}
	for range attempts {
		<-served
	}
}

// Closing one socket must not disturb the other — that is the moment an upgrade
// turns on, when the old process exits and the new one carries the traffic.
func TestClosingOneListenerLeavesTheOtherServing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SO_REUSEPORT is a unix facility")
	}

	cfg := Config{ReusePort: true}
	ctx := context.Background()

	old, err := cfg.TCP(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	addr := old.Addr().String()

	fresh, err := cfg.TCP(ctx, addr)
	if err != nil {
		t.Fatalf("second listen: %v", err)
	}
	defer func() { _ = fresh.Close() }()

	served := make(chan struct{}, 16)
	go func() {
		for {
			conn, err := fresh.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
			served <- struct{}{}
		}
	}()

	// The old process exits.
	if err := old.Close(); err != nil {
		t.Fatalf("closing the old listener: %v", err)
	}

	for i := range 10 {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("connection %d was refused after the old listener closed: %v", i, err)
		}
		_ = conn.Close()
		<-served
	}
}

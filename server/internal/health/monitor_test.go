package health

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeTarget struct {
	mu      sync.Mutex
	addr    string
	calls   []bool
	healthy bool
}

func (f *fakeTarget) Address() string { return f.addr }

func (f *fakeTarget) SetHealthy(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, v)
	f.healthy = v
}

func (f *fakeTarget) snapshot() (calls []bool, healthy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.calls...), f.healthy
}

// A listening socket proves the process is up, not that it can serve a query.
// Only pool.Server.deepCheck may promote a backend back to healthy, so this
// probe must not call SetHealthy(true) at all.
func TestReachableTargetIsNotMarkedHealthy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	target := &fakeTarget{addr: listener.Addr().String()}
	m := NewMonitor([]Target{target}, time.Hour, time.Second)

	m.checkOne(context.Background(), target)

	calls, _ := target.snapshot()
	for _, v := range calls {
		if v {
			t.Fatalf("reachable target was marked healthy; promotion belongs to deepCheck (calls=%v)", calls)
		}
	}
}

// An unreachable backend must be failed immediately — that is what the shallow
// probe is for.
func TestUnreachableTargetIsMarkedDown(t *testing.T) {
	// Bind and release, so the port is almost certainly closed.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	target := &fakeTarget{addr: addr, healthy: true}
	m := NewMonitor([]Target{target}, time.Hour, 200*time.Millisecond)

	m.checkOne(context.Background(), target)

	calls, healthy := target.snapshot()
	if len(calls) == 0 {
		t.Fatal("unreachable target was not marked down")
	}
	if healthy {
		t.Errorf("unreachable target left healthy (calls=%v)", calls)
	}
}

// A previously-failed backend must stay down until deepCheck promotes it, even
// once its socket is reachable again.
func TestRecoveredSocketDoesNotSelfPromote(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Simulate a node deepCheck has already failed.
	target := &fakeTarget{addr: listener.Addr().String(), healthy: false}
	m := NewMonitor([]Target{target}, time.Hour, time.Second)

	m.checkOne(context.Background(), target)

	if _, healthy := target.snapshot(); healthy {
		t.Error("a reachable socket promoted a failed backend back to healthy")
	}
}

func TestCheckAllVisitsEveryTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closed := listener.Addr().String()
	listener.Close()

	a := &fakeTarget{addr: closed, healthy: true}
	b := &fakeTarget{addr: closed, healthy: true}
	m := NewMonitor([]Target{a, b}, time.Hour, 200*time.Millisecond)

	m.checkAll(context.Background())

	for name, target := range map[string]*fakeTarget{"a": a, "b": b} {
		if _, healthy := target.snapshot(); healthy {
			t.Errorf("target %s was not checked", name)
		}
	}
}

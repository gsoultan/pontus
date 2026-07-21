package balancer

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

func TestPeakEWMA_Next(t *testing.T) {
	b1 := &mockBackend{address: "b1", activeConns: 0, latency: 10 * time.Millisecond, healthy: true}
	b2 := &mockBackend{address: "b2", activeConns: 0, latency: 50 * time.Millisecond, healthy: true}

	p := NewPeakEWMA([]pool.Backend{b1, b2})

	// Should pick b1 (lower latency)
	best, _ := p.Next(t.Context(), Hint{})
	if best.Address() != "b1" {
		t.Errorf("Expected b1, got %s", best.Address())
	}

	// Now increase b1 active connections
	b1.activeConns = 10
	// Cost b1 = 10ms * 11 = 110ms
	// Cost b2 = 50ms * 1 = 50ms
	// Should pick b2
	best, _ = p.Next(context.Background(), Hint{})
	if best.Address() != "b2" {
		t.Errorf("Expected b2, got %s", best.Address())
	}
}

func TestPeakEWMA_SlowStart(t *testing.T) {
	// b1 is fast but just became healthy
	b1 := &mockBackend{
		address:     "b1",
		activeConns: 0,
		latency:     10 * time.Millisecond,
		healthy:     true,
		lastHealthy: time.Now().Add(-5 * time.Second),
	}
	// b2 is slower but stable
	b2 := &mockBackend{
		address:     "b2",
		activeConns: 0,
		latency:     30 * time.Millisecond,
		healthy:     true,
		lastHealthy: time.Now().Add(-1 * time.Hour),
	}

	p := NewPeakEWMA([]pool.Backend{b1, b2})

	// CalculateCost b1: 10ms * 1 * (factor > 1)
	// CalculateCost b2: 30ms * 1 * 1
	// factor for b1 (5s elapsed) = 10 - (9 * 5 / 30) = 10 - 1.5 = 8.5
	// cost b1 = 10ms * 8.5 = 85ms
	// cost b2 = 30ms
	// Should pick b2
	best, _ := p.Next(t.Context(), Hint{})
	if best.Address() != "b2" {
		t.Errorf("Expected b2 due to slow start, got %s", best.Address())
	}
}

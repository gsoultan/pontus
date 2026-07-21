package balancer

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

func TestP2C_Next(t *testing.T) {
	b1 := &mockBackend{address: "b1", healthy: true, latency: 10 * time.Millisecond}
	b2 := &mockBackend{address: "b2", healthy: true, latency: 50 * time.Millisecond}
	b3 := &mockBackend{address: "b3", healthy: true, latency: 100 * time.Millisecond}

	p := NewP2C([]pool.Backend{b1, b2, b3})

	// Since it picks 2 random nodes, it's stochastic.
	// But it should NEVER pick b3 if b1 is one of the choices.
	// Over many runs, we should see it works.

	for range 100 {
		best, _ := p.Next(t.Context(), Hint{})
		if best == nil {
			t.Fatal("Expected a backend")
		}
	}
}

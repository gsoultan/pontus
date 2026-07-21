package balancer

import (
	"context"
	"math"
	"slices"
	"sync"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// PeakEWMA implements a latency-aware load balancing strategy.
type PeakEWMA struct {
	mu    sync.RWMutex
	nodes []pool.Backend
}

// NewPeakEWMA creates a new PeakEWMA balancer.
func NewPeakEWMA(nodes []pool.Backend) *PeakEWMA {
	return &PeakEWMA{
		nodes: slices.Clone(nodes),
	}
}

// Next returns the backend with the lowest cost (latency * (active_conns + 1)).
func (p *PeakEWMA) Next(ctx context.Context, hint Hint) (pool.Backend, error) {
	p.mu.RLock()
	nodes := p.nodes
	p.mu.RUnlock()

	if len(nodes) == 0 {
		return nil, ErrNoHealthyBackends
	}

	ptr := FilterNodes(nodes, hint)
	defer PutTargets(ptr)
	targets := *ptr
	if len(targets) == 0 {
		return nil, ErrNoHealthyBackends
	}

	var best pool.Backend
	minCost := float64(math.MaxFloat64)

	for b := range slices.Values(targets) {
		cost := CalculateCost(b, hint.CallerZone)
		if cost == 0 {
			return b, nil
		}

		if cost < minCost {
			minCost = cost
			best = b
		}
	}

	return best, nil
}

// UpdateNodes updates the backends list.
func (p *PeakEWMA) UpdateNodes(nodes []pool.Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nodes = slices.Clone(nodes)
}

func (p *PeakEWMA) Name() string {
	return "Peak EWMA (Latency-Aware)"
}

package balancer

import (
	"context"
	"math/rand/v2"
	"slices"
	"sync"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// P2C implements the Power of Two Choices load balancing strategy.
type P2C struct {
	mu    sync.RWMutex
	nodes []pool.Backend
}

// NewP2C creates a new P2C balancer.
func NewP2C(nodes []pool.Backend) *P2C {
	return &P2C{
		nodes: slices.Clone(nodes),
	}
}

// Next returns the better of two randomly chosen healthy backends.
func (p *P2C) Next(ctx context.Context, hint Hint) (pool.Backend, error) {
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

	if len(targets) == 1 {
		return targets[0], nil
	}

	// Pick two random indices
	i1 := rand.IntN(len(targets))
	i2 := rand.IntN(len(targets) - 1)
	if i2 >= i1 {
		i2++
	}

	b1 := targets[i1]
	b2 := targets[i2]

	if CalculateCost(b1, hint.CallerZone) <= CalculateCost(b2, hint.CallerZone) {
		return b1, nil
	}
	return b2, nil
}

// UpdateNodes updates the backends list.
func (p *P2C) UpdateNodes(nodes []pool.Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nodes = slices.Clone(nodes)
}

func (p *P2C) Name() string {
	return "Power of Two Choices (P2C)"
}

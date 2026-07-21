package balancer

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/gsoultan/pontus/server/internal/pool"
)

var ErrNoHealthyBackends = errors.New("no healthy backends available")

// RoundRobin implements a simple round-robin load balancing strategy.
type RoundRobin struct {
	index    atomic.Uint64
	backends sync.Map // Using sync.Map or just a Mutex might be safer, but let's use a Mutex for the whole slice
	mu       sync.RWMutex
	nodes    []pool.Backend
}

// NewRoundRobin creates a new RoundRobin balancer.
func NewRoundRobin(nodes []pool.Backend) *RoundRobin {
	return &RoundRobin{
		nodes: slices.Clone(nodes),
	}
}

// Next returns the next healthy backend in the list that matches the hint.
func (r *RoundRobin) Next(ctx context.Context, hint Hint) (pool.Backend, error) {
	r.mu.RLock()
	nodes := r.nodes
	r.mu.RUnlock()

	if len(nodes) == 0 {
		return nil, ErrNoHealthyBackends
	}

	ptr := FilterNodes(nodes, hint)
	defer PutTargets(ptr)
	targets := *ptr

	if len(targets) == 0 {
		return nil, ErrNoHealthyBackends
	}

	idx := r.index.Add(1) % uint64(len(targets))
	return targets[idx], nil
}

// UpdateNodes updates the backends list.
func (r *RoundRobin) UpdateNodes(nodes []pool.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes = slices.Clone(nodes)
}

func (r *RoundRobin) Name() string {
	return "Round Robin"
}

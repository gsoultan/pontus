package balancer

import (
	"context"
	"math"
	"slices"
	"sync"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// LeastConn implements a least-connections load balancing strategy.
type LeastConn struct {
	mu    sync.RWMutex
	nodes []pool.Backend
}

// NewLeastConn creates a new LeastConn balancer.
func NewLeastConn(nodes []pool.Backend) *LeastConn {
	return &LeastConn{
		nodes: slices.Clone(nodes),
	}
}

// Next returns the backend with the fewest active connections that matches the hint.
func (l *LeastConn) Next(ctx context.Context, hint Hint) (pool.Backend, error) {
	l.mu.RLock()
	nodes := l.nodes
	l.mu.RUnlock()

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
	minConns := int64(math.MaxInt64)

	for b := range slices.Values(targets) {
		conns := b.ActiveConns()
		if conns < minConns {
			minConns = conns
			best = b
		}
	}

	return best, nil
}

// UpdateNodes updates the backends list.
func (l *LeastConn) UpdateNodes(nodes []pool.Backend) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nodes = slices.Clone(nodes)
}

func (l *LeastConn) Name() string {
	return "Least Connections"
}

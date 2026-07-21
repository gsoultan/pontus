package balancer

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

var (
	targetsPool = sync.Pool{
		New: func() any {
			// Allocate a slice with reasonable capacity
			s := make([]pool.Backend, 0, 16)
			return &s
		},
	}
)

// Hint provides information to the balancer for selecting a backend.
type Hint struct {
	ReadOnly   bool
	Key        string
	CallerZone string
}

// Balancer defines the strategy for selecting a backend server.
type Balancer interface {
	// Next returns the next available backend according to the strategy and hint.
	Next(ctx context.Context, hint Hint) (pool.Backend, error)

	// UpdateNodes updates the list of backends available to the balancer.
	UpdateNodes(nodes []pool.Backend)

	// Name returns the name of the balancer strategy.
	Name() string
}

const (
	SlowStartDuration    = 30 * time.Second
	MaxAllowedReplicaLag = 10 * time.Second
	RemoteZonePenalty    = 2.0 // Multiplier for backends in different zones
)

// CalculateCost computes the cost of using a backend, incorporating latency, active connections, replication lag, slow start, and locality.
func CalculateCost(b pool.Backend, callerZone string) float64 {
	latency := float64(b.Latency().Nanoseconds())
	rtt := float64(b.RTT().Nanoseconds())
	if latency == 0 {
		return 0 // Prefer nodes with no data
	}

	// Use max of latency and RTT to account for both processing and network delay
	effectiveLatency := latency
	if rtt > 0 {
		// effectiveLatency = 0.7*latency + 0.3*rtt
		effectiveLatency = (latency * 0.7) + (rtt * 0.3)
	}

	active := float64(b.ActiveConns() + 1)
	weight := float64(max(b.Weight(), 1))
	cost := (effectiveLatency * active) / weight

	// Locality penalty
	if callerZone != "" && b.Zone() != "" && b.Zone() != callerZone {
		cost *= RemoteZonePenalty
	}

	// Error Rate penalty
	errorRate := b.ErrorRate()
	if errorRate > 0.05 { // More than 5% errors
		cost *= (1.0 + errorRate*5)
	}

	// Replication Lag penalty for replicas
	if b.Role() == pool.RoleReplica {
		lag := b.ReplicationLag()
		if lag > MaxAllowedReplicaLag {
			cost *= 100 // Heavy penalty for high lag
		} else if lag > 0 {
			// Linear penalty for lag: 1x at 0s, 2x at MaxAllowedReplicaLag
			factor := 1.0 + (float64(lag) / float64(MaxAllowedReplicaLag))
			cost *= factor
		}
	}

	// Slow Start
	if lastHealthy := b.LastHealthy(); !lastHealthy.IsZero() {
		elapsed := time.Since(lastHealthy)
		if elapsed < SlowStartDuration {
			// Scale cost linearly from 10x to 1x over the slow start duration
			factor := 10.0 - (9.0 * float64(elapsed) / float64(SlowStartDuration))
			cost *= factor
		}
	}

	return cost
}

// FilterNodes returns a list of healthy backends that match the hint.
// It uses a pool to minimize allocations in the hot path.
func FilterNodes(nodes []pool.Backend, hint Hint) *[]pool.Backend {
	p, ok := targetsPool.Get().(*[]pool.Backend)
	if !ok {
		s := make([]pool.Backend, 0, 16)
		p = &s
	}
	*p = (*p)[:0]
	targets := *p

	if hint.ReadOnly {
		// Prefer replicas for read-only, but exclude those with high lag or draining
		for node := range slices.Values(nodes) {
			if node.Role() == pool.RoleReplica && node.IsHealthy() && !node.IsDraining() && node.ReplicationLag() <= MaxAllowedReplicaLag {
				targets = append(targets, node)
			}
		}
		// Fallback to high-lag replicas if no healthy low-lag replicas
		if len(targets) == 0 {
			for node := range slices.Values(nodes) {
				if node.Role() == pool.RoleReplica && node.IsHealthy() && !node.IsDraining() {
					targets = append(targets, node)
				}
			}
		}
		// If still no healthy replicas, fallback to primary
		if len(targets) == 0 {
			for node := range slices.Values(nodes) {
				if node.Role() == pool.RolePrimary && node.IsHealthy() && !node.IsDraining() {
					targets = append(targets, node)
				}
			}
		}
	} else {
		// Must use primary for writes. Enforce exactly one primary if possible to avoid split-brain.
		var bestPrimary pool.Backend
		for node := range slices.Values(nodes) {
			if node.Role() == pool.RolePrimary && node.IsHealthy() && !node.IsDraining() {
				if bestPrimary == nil || CalculateCost(node, hint.CallerZone) < CalculateCost(bestPrimary, hint.CallerZone) {
					bestPrimary = node
				}
			}
		}
		if bestPrimary != nil {
			targets = append(targets, bestPrimary)
		}
	}

	// Update the pool pointer with the new capacity if it grew
	*p = targets
	return p
}

// PutTargets returns a slice pointer to the pool.
func PutTargets(p *[]pool.Backend) {
	if p != nil {
		targetsPool.Put(p)
	}
}

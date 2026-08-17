package balancer

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
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
	SlowStartDuration = 30 * time.Second
	RemoteZonePenalty = 2.0 // Multiplier for backends in different zones

	// DefaultMaxReplicaLag is used until configuration sets one.
	DefaultMaxReplicaLag = 10 * time.Second
)

// maxReplicaLag is how far a replica may fall behind before reads stop going to
// it — pgpool-II's delay_threshold.
//
// Package-level and atomic rather than a parameter: it is read on the query
// path by FilterNodes and CalculateCost across five strategies and eleven call
// sites, so threading it through would put a config struct in every balancer
// signature for a value that changes only on reload. An atomic load is free
// here and, unlike the plain variable it would otherwise be, it does not race
// with a hot reload.
var maxReplicaLag atomic.Int64

func init() { maxReplicaLag.Store(int64(DefaultMaxReplicaLag)) }

// SetMaxReplicaLag updates the threshold. Non-positive values restore the
// default rather than disabling the gate, because a zero threshold would route
// reads to every replica no matter how far behind it is.
func SetMaxReplicaLag(d time.Duration) {
	if d <= 0 {
		d = DefaultMaxReplicaLag
	}
	maxReplicaLag.Store(int64(d))
}

// MaxReplicaLag returns the current threshold.
func MaxReplicaLag() time.Duration {
	return time.Duration(maxReplicaLag.Load())
}

// CalculateCost computes the cost of using a backend, incorporating latency, active connections, replication lag, slow start, and locality.
func CalculateCost(b pool.Backend, callerZone string) float64 {
	latency := float64(b.Latency().Nanoseconds())
	rtt := float64(b.RTT().Nanoseconds())

	// Blend service time with time-to-first-byte: the first covers the work, the
	// second the network.
	effectiveLatency := latency
	switch {
	case latency > 0 && rtt > 0:
		effectiveLatency = (latency * 0.7) + (rtt * 0.3)
	case latency == 0:
		effectiveLatency = rtt
	}

	// No timing yet — a backend that has served nothing, or one whose first
	// request is still in flight.
	//
	// This used to `return 0`, which did not mean "prefer it": it meant every
	// term below was discarded, so connections, weight, locality, error rate and
	// replication lag stopped counting entirely. With nothing reporting latency
	// that was every backend, all the time, and least_conn, p2c and peak_ewma
	// each ranked by a constant. A neutral unit keeps the remaining terms
	// meaningful while still favouring an untried node over a slow one.
	if effectiveLatency <= 0 {
		effectiveLatency = 1
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
		maxLag := MaxReplicaLag()
		lag := b.ReplicationLag()
		if lag > maxLag {
			cost *= 100 // Heavy penalty for high lag
		} else if lag > 0 {
			// Linear penalty for lag: 1x at 0s, 2x at the threshold
			factor := 1.0 + (float64(lag) / float64(maxLag))
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
		maxLag := MaxReplicaLag()
		// Prefer replicas for read-only, but exclude those with high lag or draining
		for node := range slices.Values(nodes) {
			// IsReplicating is the streaming check: a replica whose WAL receiver
			// has gone is up, healthy and answering, and every row it returns is
			// older than the last. Lag alone does not catch it — once it has
			// replayed the little it received, receive==replay and it reports
			// zero.
			if node.Role() == pool.RoleReplica && node.IsHealthy() && !node.IsDraining() &&
				node.IsReplicating() && node.ReplicationLag() <= maxLag {
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

package orchestration

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/internal/pool"
)

func (m *FailoverManager) ProvisionReplica(ctx context.Context, req *endpoints.ProvisionReplicaRequest, progress chan<- *endpoints.ProvisionProgress) error {
	return m.provisioner.ProvisionReplica(ctx, req, progress)
}

type FailoverState int

const (
	StateIdle FailoverState = iota
	StateMonitoring
	StatePromoting
	StateVerifying
	StateFailed
)

type FailoverManager struct {
	provisioner Provisioner
	consensus   Consensus
	backends    func() []pool.Backend
	mu          sync.Mutex
	state       FailoverState
}

func NewFailoverManager(provisioner Provisioner, consensus Consensus, backends func() []pool.Backend) *FailoverManager {
	return &FailoverManager{
		provisioner: provisioner,
		consensus:   consensus,
		backends:    backends,
		state:       StateIdle,
	}
}

func (m *FailoverManager) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.monitor(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (m *FailoverManager) monitor(ctx context.Context) {
	m.mu.Lock()
	if m.state == StatePromoting {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	// Only leader handles failover logic
	if m.consensus != nil && !m.consensus.IsLeader() {
		return
	}

	backends := m.backends()
	var healthyPrimaries []pool.Backend
	var allPrimaries []pool.Backend
	var healthyReplicas []pool.Backend

	for _, b := range backends {
		if b.Role() == pool.RolePrimary {
			allPrimaries = append(allPrimaries, b)
			if b.IsHealthy() {
				healthyPrimaries = append(healthyPrimaries, b)
			}
		} else if b.Role() == pool.RoleReplica && b.IsHealthy() {
			healthyReplicas = append(healthyReplicas, b)
		}
	}

	// 1. Automatic Failover: No healthy primary
	if len(healthyPrimaries) == 0 {
		slog.Warn("No healthy primary detected, triggering automatic failover")
		if err := m.TriggerFailover(ctx); err != nil {
			slog.Error("Automatic failover failed", "error", err)
		}
		return
	}

	// 2. Automatic Failback / Self-Healing: Check for recovered nodes that think they are primary
	// or replicas that are now healthy and could be promoted back if they were the preferred primary.
	// For now, let's focus on split-brain prevention and basic failback.
	if len(allPrimaries) > 1 {
		slog.Error("Multiple primaries detected! Split-brain scenario.", "count", len(allPrimaries))
		// Use consensus to decide who is the real primary
		consensusPrimary, _ := m.consensus.GetPrimary()

		var winner pool.Backend
		if consensusPrimary != "" {
			for _, p := range allPrimaries {
				if p.Address() == consensusPrimary {
					winner = p
					break
				}
			}
		}
		if winner == nil {
			winner = healthyPrimaries[0]
		}

		for _, p := range allPrimaries {
			if p.Address() == winner.Address() {
				continue
			}
			slog.Warn("Demoting extra primary to replica (Self-Healing)", "address", p.Address(), "current_primary", winner.Address())
			if err := m.provisioner.DemoteToReplica(ctx, p.Address(), winner.Address()); err != nil {
				slog.Error("Failed to demote primary", "address", p.Address(), "error", err)
			} else {
				p.ReevaluateRole()
			}
		}
	}
}

// setState stores the failover state under the lock.
//
// The transitions on the error and success paths below used to assign
// m.state directly while monitor(), State() and the deferred reset read it
// under m.mu — a data race on the variable that decides whether a failover is
// already running, which is the last place a torn read is acceptable.
func (m *FailoverManager) setState(state FailoverState) {
	m.mu.Lock()
	m.state = state
	m.mu.Unlock()
}

func (m *FailoverManager) TriggerFailover(ctx context.Context) error {
	m.mu.Lock()
	if m.state == StatePromoting {
		m.mu.Unlock()
		return fmt.Errorf("failover already in progress")
	}
	m.state = StatePromoting
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		if m.state == StatePromoting {
			m.state = StateIdle
		}
		m.mu.Unlock()
	}()

	slog.Warn("Failover triggered, searching for best replica to promote")

	// 1. Find best replica (lowest lag)
	bestReplica, err := m.findBestReplica(ctx)
	if err != nil {
		m.setState(StateFailed)
		return fmt.Errorf("failed to find replica to promote: %w", err)
	}

	slog.Info("Promoting replica", "address", bestReplica.Address())

	// 2. Promote
	if err := m.provisioner.PromoteToPrimary(ctx, bestReplica.Address()); err != nil {
		m.setState(StateFailed)
		return fmt.Errorf("failed to promote %s: %w", bestReplica.Address(), err)
	}

	// 3. Update Consensus
	if m.consensus != nil {
		if err := m.consensus.SetPrimary(bestReplica.Address()); err != nil {
			slog.Error("Failed to update consensus with new primary", "error", err)
		}
	}

	slog.Info("Replica promoted successfully", "address", bestReplica.Address())
	m.setState(StateVerifying)

	// Reset to idle after verification period (simulated)
	go func() {
		time.Sleep(30 * time.Second)
		m.mu.Lock()
		if m.state == StateVerifying {
			m.state = StateIdle
		}
		m.mu.Unlock()
	}()

	return nil
}

func (m *FailoverManager) findBestReplica(ctx context.Context) (pool.Backend, error) {
	backends := m.backends()
	var best pool.Backend
	var minLag time.Duration = -1

	for _, b := range backends {
		if b.Role() != pool.RoleReplica || !b.IsHealthy() {
			continue
		}

		lag, err := m.provisioner.CheckReplicationLag(ctx, b.Address())
		if err != nil {
			slog.Warn("Failed to check lag for backend", "address", b.Address(), "error", err)
			continue
		}

		if minLag == -1 || lag < minLag {
			minLag = lag
			best = b
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no healthy replicas found")
	}

	return best, nil
}

func (m *FailoverManager) State() FailoverState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

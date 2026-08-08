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

	// lastPromoted is the node this manager most recently made primary. It is
	// the tie-breaker when an old primary comes back and there is no consensus
	// to ask, because config order alone would hand the write role back to the
	// node that just failed.
	lastPromoted string

	// missedPrimary counts consecutive checks that found no healthy primary.
	// Promotion is irreversible, so it waits for opts.FailureThreshold of these
	// rather than acting on a single observation.
	missedPrimary int

	opts Options
}

func NewFailoverManager(provisioner Provisioner, consensus Consensus, backends func() []pool.Backend, opts Options) *FailoverManager {
	return &FailoverManager{
		provisioner: provisioner,
		consensus:   consensus,
		backends:    backends,
		state:       StateIdle,
		opts:        opts.sane(),
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

	// 1. Automatic failover: no healthy primary.
	//
	// Gated on consecutive observations rather than one. Promotion cannot be
	// undone — PostgreSQL starts a new timeline and the old primary can no
	// longer simply rejoin — so a failover triggered by a dropped packet costs
	// far more than one that lands a few seconds late. pgpool-II makes the same
	// trade with health_check_max_retries.
	if len(healthyPrimaries) == 0 {
		m.mu.Lock()
		m.missedPrimary++
		missed := m.missedPrimary
		m.mu.Unlock()

		switch {
		case !m.opts.Enabled:
			slog.Warn("No healthy primary detected; automatic failover is disabled",
				"consecutive_checks", missed)
		case missed < m.opts.FailureThreshold:
			slog.Warn("No healthy primary detected; waiting for confirmation before promoting",
				"consecutive_checks", missed, "threshold", m.opts.FailureThreshold)
		default:
			slog.Warn("No healthy primary after repeated checks, triggering automatic failover",
				"consecutive_checks", missed, "threshold", m.opts.FailureThreshold)
			if err := m.TriggerFailover(ctx); err != nil {
				slog.Error("Automatic failover failed", "error", err)
			}
		}
		return
	}

	// A primary answered, so any earlier misses were a blip.
	m.mu.Lock()
	m.missedPrimary = 0
	m.mu.Unlock()

	// 2. Split-brain resolution.
	//
	// This is what an old primary coming back after a failover looks like: it
	// reboots, Postgres starts, and it still believes it is the primary. Two
	// primaries is the ordinary end of a failover, not an exotic case.
	//
	// Note this is *not* failback — the recovered node is demoted to a replica
	// and the write role stays where the failover put it. Returning the write
	// role to a preferred node is a separate, deliberate operation.
	if len(allPrimaries) > 1 {
		slog.Error("Multiple primaries detected! Split-brain scenario.", "count", len(allPrimaries))

		// Consensus is nil in a single-node deployment — registry.go constructs
		// the manager that way — and asking a nil Consensus for the primary
		// wedges this goroutine, which is the one running failover detection.
		// The leader check above already guards for nil; this call did not.
		var consensusPrimary string
		if m.consensus != nil {
			consensusPrimary, _ = m.consensus.GetPrimary()
		}

		var winner pool.Backend
		if consensusPrimary != "" {
			for _, p := range allPrimaries {
				if p.Address() == consensusPrimary {
					winner = p
					break
				}
			}
		}
		// With no consensus to appeal to, prefer the node this manager promoted.
		// Config order would pick the original primary, so a node that failed
		// and came back would win against the replica promoted to replace it —
		// silently undoing the failover and sending writes to the node that
		// just failed, after a replica has already taken writes.
		if winner == nil {
			m.mu.Lock()
			promoted := m.lastPromoted
			m.mu.Unlock()

			for _, p := range allPrimaries {
				if p.Address() == promoted && p.IsHealthy() {
					winner = p
					break
				}
			}
		}
		if winner == nil {
			if len(healthyPrimaries) == 0 {
				slog.Error("Split-brain with no healthy primary; leaving roles alone")
				return
			}
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

	m.mu.Lock()
	m.lastPromoted = bestReplica.Address()
	m.mu.Unlock()

	// 4. Tell the data plane the write node moved.
	//
	// Promotion happens on the database host; the pool only learns a role
	// changed when its own 30s deep check next runs. Until then the proxy still
	// has the promoted node recorded as a replica and the dead node recorded as
	// the primary, so every write goes to the backend that just failed — for up
	// to half a minute after the log line above says the failover succeeded.
	//
	// ReevaluateRole is a non-blocking nudge to the pool's role checker, so this
	// is safe to call while holding nothing.
	bestReplica.ReevaluateRole()
	for _, b := range m.backends() {
		if b.Role() == pool.RolePrimary && b.Address() != bestReplica.Address() {
			b.ReevaluateRole()
		}
	}

	// The promotion itself has landed and the write path is restored, so the
	// counter resets here rather than waiting for the next healthy observation.
	m.mu.Lock()
	m.missedPrimary = 0
	m.mu.Unlock()

	slog.Info("Replica promoted successfully", "address", bestReplica.Address())
	m.setState(StateVerifying)

	// 5. Re-point the surviving replicas.
	//
	// Detached from the caller's context on purpose. Re-pointing a replica can
	// mean a full base backup, and this runs from a 5s monitor tick or from a
	// request-scoped context in the gateway — either would cancel the rebuild
	// part-way and leave the replica in a worse state than before. The per-node
	// timeout inside is what bounds it.
	if m.opts.FollowPrimary {
		go m.followNewPrimary(context.WithoutCancel(ctx),
			bestReplica.Address(), m.opts.FollowPrimaryTimeout)
	}

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

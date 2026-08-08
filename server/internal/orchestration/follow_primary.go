package orchestration

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// followNewPrimary re-points every surviving replica at the node just promoted.
//
// Promotion on its own only fixes the write path. Every other replica is still
// streaming from the primary that just died, so it stops receiving WAL, drifts,
// and keeps serving reads that get staler by the minute — silently, because the
// node is up and answers queries perfectly well. On a two-node cluster this does
// not arise; from three nodes up, a failover without this step leaves the
// cluster broken in a way nothing surfaces. pgpool-II gives it a dedicated hook
// (follow_primary_command) for the same reason.
//
// The old primary is deliberately not touched. It has diverged onto an
// abandoned timeline and needs pg_rewind or a rebuild, which is an operator
// decision, not something to start automatically underneath a failover.
func (m *FailoverManager) followNewPrimary(ctx context.Context, primary string, timeout time.Duration) {
	targets := m.replicasToFollow(primary)
	if len(targets) == 0 {
		return
	}

	slog.Info("Re-pointing replicas at the new primary",
		"primary", primary, "replicas", len(targets))

	var wg sync.WaitGroup
	for _, replica := range targets {
		addr := replica.Address()
		wg.Go(func() {
			// Each replica gets its own budget: one slow rebuild must not eat
			// the time the others need.
			nodeCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			if err := m.provisioner.DemoteToReplica(nodeCtx, addr, primary); err != nil {
				// Not fatal to the failover — the write path is already
				// restored. This replica is now orphaned and needs an operator,
				// so say so plainly rather than folding it into a debug line.
				slog.Error("Replica could not be re-pointed at the new primary; "+
					"it is still following the old one and will serve increasingly stale reads",
					"replica", addr, "primary", primary, "error", err)
				return
			}
			slog.Info("Replica now follows the new primary", "replica", addr, "primary", primary)
		})
	}
	wg.Wait()
}

// replicasToFollow lists the nodes that should be re-pointed.
//
// Unhealthy replicas are left alone on purpose: re-pointing one means dialling
// its agent and very likely running a base backup, and doing that against a
// node that is already down burns the timeout for no result. Those come back
// through recovery, not through this path.
func (m *FailoverManager) replicasToFollow(primary string) []pool.Backend {
	var targets []pool.Backend
	for _, b := range m.backends() {
		if b.Address() == primary || b.Role() != pool.RoleReplica {
			continue
		}
		if !b.IsHealthy() {
			slog.Warn("Replica is down and was not re-pointed at the new primary; "+
				"it needs recovery before it can rejoin",
				"replica", b.Address(), "primary", primary)
			continue
		}
		targets = append(targets, b)
	}
	return targets
}

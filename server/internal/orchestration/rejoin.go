package orchestration

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/server/internal/pool"
)

// rejoinState is what has been tried for one node.
type rejoinState struct {
	attempts    int
	lastAttempt time.Time
	inFlight    bool
}

// rejoinTracker records rebuild attempts per node.
//
// Bounded by the configured backends, not by anything a client sends: entries
// are only ever created for an address the operator listed.
type rejoinTracker struct {
	mu    sync.Mutex
	nodes map[string]*rejoinState
}

func newRejoinTracker() *rejoinTracker {
	return &rejoinTracker{nodes: make(map[string]*rejoinState)}
}

// begin claims a node for one attempt, reporting whether the caller may
// proceed. Refuses when an attempt is already running, when the last one was
// too recent, or when the node has used its budget.
func (t *rejoinTracker) begin(addr string, interval time.Duration, maxAttempts int, now time.Time) (ok, exhausted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, found := t.nodes[addr]
	if !found {
		state = &rejoinState{}
		t.nodes[addr] = state
	}

	if state.inFlight {
		return false, false
	}
	if state.attempts >= maxAttempts {
		return false, true
	}
	// The zero time is "never tried", which must not be read as "tried a very
	// long time ago and therefore due" — it is, but only because the
	// subtraction happens to be large, and relying on that is how a bug lands
	// the first time someone changes the clock source.
	if found && !state.lastAttempt.IsZero() && now.Sub(state.lastAttempt) < interval {
		return false, false
	}

	state.inFlight = true
	state.lastAttempt = now
	state.attempts++
	return true, false
}

// finish releases a node. A success clears its history, so a node that fails,
// is rebuilt, and later fails again gets the full budget the second time rather
// than inheriting a count from an outage that is over.
func (t *rejoinTracker) finish(addr string, succeeded bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.nodes[addr]
	if !ok {
		return
	}
	state.inFlight = false
	if succeeded {
		delete(t.nodes, addr)
	}
}

// forget drops a node's history once it is healthy and streaming again, whether
// or not Pontus is what fixed it. An operator who rebuilds a node by hand has
// resolved the problem, and the next failure should start from a clean budget.
func (t *rejoinTracker) forget(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if state, ok := t.nodes[addr]; ok && !state.inFlight {
		delete(t.nodes, addr)
	}
}

// needsRejoin reports whether a node is reachable but no longer part of the
// cluster's replication.
//
// Reachable is the load-bearing half. A node Pontus cannot reach might be
// rebooting, and rebuilding it is both impossible — the rebuild runs through
// its agent — and destructive if it were. What this catches is the node that is
// up, answers queries, and has no WAL receiver: a former primary back on an
// abandoned timeline, or a replica whose replication broke. Those look healthy
// from every angle except the one that matters.
//
// A primary always reports itself as replicating, so this never selects one.
// Two primaries is split-brain, which is resolved before this runs.
func needsRejoin(b pool.Backend, primary string) bool {
	return b.Address() != primary &&
		b.Role() == pool.RoleReplica &&
		b.IsHealthy() &&
		!b.IsReplicating() &&
		!b.IsDraining()
}

// reconcileRejoins rebuilds the nodes that have fallen out of replication.
//
// Called from the monitor tick with a healthy primary in hand. Each rebuild
// runs in its own goroutine because it can take as long as a base backup, and
// the monitor is the loop that also detects failover — blocking it for half an
// hour would mean a second outage went unnoticed while the first was being
// repaired.
func (m *FailoverManager) reconcileRejoins(ctx context.Context, primary string, backends []pool.Backend) {
	var pending int
	for _, b := range backends {
		if !needsRejoin(b, primary) {
			m.rejoins.forget(b.Address())
			continue
		}
		pending++

		if !m.opts.AutoRejoin {
			slog.Warn("A node is reachable but not replicating; automatic rejoin is disabled",
				"node", b.Address(), "primary", primary)
			continue
		}

		ok, exhausted := m.rejoins.begin(b.Address(), m.opts.AutoRejoinInterval,
			m.opts.AutoRejoinMaxAttempts, time.Now())
		if exhausted {
			observability.RejoinResults.WithLabelValues("exhausted").Inc()
			slog.Error("Giving up on rebuilding a node; it needs an operator",
				"node", b.Address(), "attempts", m.opts.AutoRejoinMaxAttempts,
				"primary", primary)
			continue
		}
		if !ok {
			continue
		}

		go m.rejoin(ctx, b, primary)
	}
	observability.RejoinPending.Set(float64(pending))
}

// rejoin rebuilds one node as a replica of the current primary.
func (m *FailoverManager) rejoin(ctx context.Context, node pool.Backend, primary string) {
	addr := node.Address()

	slog.Warn("Rebuilding a node that is reachable but not replicating",
		"node", addr, "primary", primary, "timeout", m.opts.AutoRejoinTimeout)

	nodeCtx, cancel := context.WithTimeout(ctx, m.opts.AutoRejoinTimeout)
	defer cancel()

	if err := m.provisioner.DemoteToReplica(nodeCtx, addr, primary); err != nil {
		m.rejoins.finish(addr, false)
		observability.RejoinResults.WithLabelValues("error").Inc()
		slog.Error("Could not rebuild the node; it is still not replicating and serves no reads",
			"node", addr, "primary", primary, "error", err)
		return
	}

	// The rebuild happened on the database host, so the pool still believes
	// what it last measured. Without this the node waits for its own deep-check
	// tick before anything here can see what changed — the same lag that made a
	// promotion take half a minute to reach the proxy.
	node.ReevaluateRole()

	// Confirm the outcome rather than trusting the report.
	//
	// The agent tells Pontus it succeeded; that is a claim from another
	// process about work Pontus cannot see. A stubbed or half-implemented
	// agent that answers "Replication configured" having done nothing is
	// indistinguishable from a working one at this point — and that is not
	// hypothetical, it is what the shipped agent does today. Treating the
	// claim as the result means a node is marked recovered, its attempt
	// history cleared, and the cluster reported healthy while it serves
	// nothing.
	//
	// What matters is observable from here: the node is a replica again and
	// its WAL receiver is attached.
	if !m.confirmRejoined(ctx, node) {
		m.rejoins.finish(addr, false)
		observability.RejoinResults.WithLabelValues("error").Inc()
		slog.Error("The agent reported a successful rebuild, but the node is still not "+
			"replicating; treating it as failed",
			"node", addr, "primary", primary, "role", node.Role())
		return
	}

	m.rejoins.finish(addr, true)
	observability.RejoinResults.WithLabelValues("ok").Inc()

	slog.Info("Node rebuilt and following the primary again",
		"node", addr, "primary", primary)
}

// rejoinConfirmTimeout bounds the wait for a rebuilt node to start streaming.
//
// A base backup has already finished by this point; what remains is the node
// restarting and its WAL receiver attaching, which is seconds. Waiting longer
// would hold the attempt open and stop the next tick from retrying.
const rejoinConfirmTimeout = 30 * time.Second

// confirmRejoined reports whether the node is now a replica that is streaming.
func (m *FailoverManager) confirmRejoined(ctx context.Context, node pool.Backend) bool {
	deadline := time.Now().Add(rejoinConfirmTimeout)
	for {
		if node.Role() == pool.RoleReplica && node.IsReplicating() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
			node.ReevaluateRole()
		}
	}
}

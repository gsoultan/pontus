package pool

import (
	"sync/atomic"
	"time"
)

// Auto-reattach governs when a replica that stopped replicating is allowed back
// into the read pool.
//
// This is Pontus's version of pgpool-II's auto_failback, and it deliberately
// differs on what "off" means. pgpool's flag guards against the pooler putting
// back a node an operator had *administratively detached* for maintenance.
// Pontus has no administrative detach — SetDraining exists on the interface but
// nothing calls it and no RPC exposes it — so "never re-admit automatically"
// would mean "stays out until the process restarts", with no way back. That is
// a worse default than the problem it solves.
//
// So here:
//
//	auto_reattach: true   (default) a replica with no WAL receiver is pulled
//	                      from the read pool, and re-admitted only after it has
//	                      been streaming continuously for auto_reattach_interval
//	auto_reattach: false  the pooler does not consider streaming state; reads
//	                      are gated on lag alone, which is the older behaviour
//
// If an administrative detach is added later, pgpool's exact semantics become
// available and this comment should be revisited.
var (
	autoReattach     atomic.Bool
	reattachInterval atomic.Int64
)

// DefaultReattachInterval is how long a replica must stream cleanly before it
// is trusted with reads again. Short enough not to matter during a real
// recovery, long enough that a node flapping between connected and disconnected
// does not keep re-entering the read path.
const DefaultReattachInterval = time.Minute

func init() {
	autoReattach.Store(true)
	reattachInterval.Store(int64(DefaultReattachInterval))
}

// SetAutoReattach configures the policy. A non-positive interval restores the
// default rather than admitting a replica the instant it reconnects.
func SetAutoReattach(enabled bool, dwell time.Duration) {
	if dwell <= 0 {
		dwell = DefaultReattachInterval
	}
	autoReattach.Store(enabled)
	reattachInterval.Store(int64(dwell))
}

// AutoReattachPolicy reports the current settings.
func AutoReattachPolicy() (enabled bool, dwell time.Duration) {
	return autoReattach.Load(), time.Duration(reattachInterval.Load())
}

// IsReplicating reports whether this backend is fit to serve reads as a replica.
//
// A primary always is. A replica is only once it has a WAL receiver attached
// and has held it for the dwell interval — a node that reconnects, catches a
// few segments, and drops again is worse than one that stays out, because each
// brief reappearance pulls traffic onto data that is about to go stale again.
func (p *Server) IsReplicating() bool {
	if p.Role() != RoleReplica {
		return true
	}
	enabled, dwell := AutoReattachPolicy()
	if !enabled {
		return true
	}
	if !p.replicating.Load() {
		return false
	}

	// The dwell is a penalty for *recovering*, not for being newly observed.
	// A replica that has been streaming for a week is not suspect because
	// Pontus started thirty seconds ago, and charging it the interval anyway
	// took every replica out of the read pool for a minute after every restart
	// and every config reload.
	if !p.stoppedReplicating.Load() {
		return true
	}

	since := p.replicatingSince.Load()
	if since == 0 {
		return false
	}
	return time.Since(time.Unix(0, since)) >= dwell
}

// setReplicating records the observed streaming state, starting the dwell clock
// on the transition into streaming and clearing it on the way out.
func (p *Server) setReplicating(streaming bool) {
	if !streaming {
		p.replicating.Store(false)
		p.replicatingSince.Store(0)
		// Remember that this node has dropped at least once, so its next
		// recovery has to wait out the dwell.
		p.stoppedReplicating.Store(true)
		return
	}
	// Only the first observation in a run starts the clock; later ones must not
	// push it forward or the dwell would never elapse.
	if p.replicating.CompareAndSwap(false, true) {
		p.replicatingSince.Store(time.Now().UnixNano())
	}
}

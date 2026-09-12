package orchestration

import "time"

// Options tunes the failover manager.
//
// Deliberately a plain struct rather than a pkg/config import: nothing under
// server/internal reads the config file directly, the registry translates it.
// Keeping that direction means this package can be tested without a config
// tree, and a YAML rename cannot reach into the data plane.
type Options struct {
	// Enabled turns automatic promotion on. Split-brain resolution and state
	// reporting continue regardless; only promotion is gated.
	Enabled bool

	// FailureThreshold is how many consecutive checks must find no healthy
	// primary before promoting.
	FailureThreshold int

	// FollowPrimary re-points surviving replicas after a promotion.
	FollowPrimary bool

	// FollowPrimaryTimeout bounds re-pointing a single replica.
	FollowPrimaryTimeout time.Duration

	// AutoReattach lets a replica marked down rejoin once its replication is
	// demonstrably working. Replicas only — never the write role.
	AutoReattach bool

	// AutoReattachInterval is the minimum gap between two reattachments.
	AutoReattachInterval time.Duration

	// AutoRejoin rebuilds a node that is reachable but no longer replicating,
	// as a replica of the current primary. Replicas only — never the write
	// role. Off by default because a rebuild can discard a data directory.
	AutoRejoin bool

	// AutoRejoinInterval is the minimum gap between two attempts on one node.
	AutoRejoinInterval time.Duration

	// AutoRejoinTimeout bounds a single attempt.
	AutoRejoinTimeout time.Duration

	// AutoRejoinMaxAttempts is how many rebuilds one node gets before it is
	// left to an operator.
	AutoRejoinMaxAttempts int
}

// sane fills in anything a caller left at zero, so the manager never has to
// interpret a zero as "off" in one place and "default" in another.
func (o Options) sane() Options {
	if o.FailureThreshold <= 0 {
		o.FailureThreshold = 3
	}
	if o.FollowPrimaryTimeout <= 0 {
		o.FollowPrimaryTimeout = 30 * time.Minute
	}
	if o.AutoReattachInterval <= 0 {
		o.AutoReattachInterval = time.Minute
	}
	if o.AutoRejoinInterval <= 0 {
		o.AutoRejoinInterval = 5 * time.Minute
	}
	if o.AutoRejoinTimeout <= 0 {
		o.AutoRejoinTimeout = 30 * time.Minute
	}
	if o.AutoRejoinMaxAttempts <= 0 {
		o.AutoRejoinMaxAttempts = 3
	}
	return o
}

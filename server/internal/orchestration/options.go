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
	return o
}

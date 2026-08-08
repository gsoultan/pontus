package config

import "time"

// Failover tunes automatic promotion and the recovery that follows it.
//
// The defaults are deliberately conservative in the same direction pgpool-II
// picked: promoting is disruptive and irreversible — PostgreSQL starts a new
// timeline, so the old primary cannot simply rejoin — which makes a failover
// that fires on a transient blip worse than one that fires a few seconds late.
type Failover struct {
	// Enabled turns automatic promotion on. With it off the manager still
	// resolves split-brain and still reports state; it just never promotes.
	Enabled bool `json:"enabled,omitzero" yaml:"enabled"`

	// FailureThreshold is how many consecutive checks must find no healthy
	// primary before a replica is promoted. This is pgpool-II's
	// health_check_max_retries: retries, not a single observation, are what
	// separate a dead primary from a spotty network. Zero takes the default.
	FailureThreshold int `json:"failure_threshold,omitzero" yaml:"failure_threshold"`

	// FollowPrimary re-points surviving replicas at the newly promoted primary
	// after a failover. Without it every other replica keeps streaming from the
	// node that just died, which leaves a cluster of three or more silently
	// broken. This is pgpool-II's follow_primary_command.
	FollowPrimary bool `json:"follow_primary,omitzero" yaml:"follow_primary"`

	// FollowPrimaryTimeout bounds re-pointing a single replica. Rebuilding one
	// can mean a pg_basebackup of the whole cluster, so this is generous by
	// default. Zero takes the default.
	FollowPrimaryTimeout time.Duration `json:"follow_primary_timeout,omitzero" yaml:"follow_primary_timeout"`

	// MaxReplicaLag is how far a replica may fall behind before reads stop
	// being routed to it. pgpool-II calls this delay_threshold. Zero takes the
	// default.
	MaxReplicaLag time.Duration `json:"max_replica_lag,omitzero" yaml:"max_replica_lag"`

	// AutoReattach lets a replica that was marked down rejoin on its own once
	// its replication is demonstrably working again.
	//
	// This is pgpool-II's auto_failback, and like pgpool's it applies to
	// replicas only — nothing here ever hands the write role back to a
	// recovered primary. That node has diverged onto an old timeline and needs
	// pg_rewind or a rebuild, so returning it to service is an operator action.
	//
	// Default off, as in pgpool: if you detached a node to work on it, you do
	// not want the pooler putting it back under load.
	AutoReattach bool `json:"auto_reattach,omitzero" yaml:"auto_reattach"`

	// AutoReattachInterval is the minimum gap between two automatic
	// reattachments, so a node that keeps flapping cannot be re-added on every
	// check. Zero takes the default.
	AutoReattachInterval time.Duration `json:"auto_reattach_interval,omitzero" yaml:"auto_reattach_interval"`
}

// Defaults for the failover block. Every one of these is a documented tunable
// rather than a literal buried at a call site.
const (
	DefaultFailureThreshold     = 3
	DefaultFollowPrimaryTimeout = 30 * time.Minute
	DefaultMaxReplicaLag        = 10 * time.Second
	DefaultAutoReattachInterval = time.Minute
)

// withDefaults returns a copy with every zero-valued tunable filled in.
func (f *Failover) withDefaults() Failover {
	out := Failover{}
	if f != nil {
		out = *f
	}
	if out.FailureThreshold <= 0 {
		out.FailureThreshold = DefaultFailureThreshold
	}
	if out.FollowPrimaryTimeout <= 0 {
		out.FollowPrimaryTimeout = DefaultFollowPrimaryTimeout
	}
	if out.MaxReplicaLag <= 0 {
		out.MaxReplicaLag = DefaultMaxReplicaLag
	}
	if out.AutoReattachInterval <= 0 {
		out.AutoReattachInterval = DefaultAutoReattachInterval
	}
	return out
}

// FailoverOptions returns the failover block with defaults applied, so callers
// never have to decide what a zero means.
func (c *Options) FailoverOptions() Failover {
	return c.Failover.withDefaults()
}

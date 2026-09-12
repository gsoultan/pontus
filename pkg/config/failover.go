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
	// Unlike pgpool this defaults to ON, and "off" means something different —
	// see server/internal/pool/reattach.go. pgpool's flag guards a node an
	// operator administratively detached; Pontus has no detach, so "never
	// re-admit" would mean "out until restart, with no way back". Here off
	// simply means routing ignores streaming state and gates on lag alone.
	AutoReattach *bool `json:"auto_reattach,omitzero" yaml:"auto_reattach"`

	// AutoReattachInterval is the minimum gap between two automatic
	// reattachments, so a node that keeps flapping cannot be re-added on every
	// check. Zero takes the default.
	AutoReattachInterval time.Duration `json:"auto_reattach_interval,omitzero" yaml:"auto_reattach_interval"`

	// AutoRejoin rebuilds a node that is reachable but no longer replicating,
	// as a replica of the current primary.
	//
	// This is the other half of AutoReattach. AutoReattach stops routing reads
	// to a node whose replication has stopped; nothing then fixes it, so the
	// cluster runs permanently short until an operator notices. A former
	// primary that comes back after a failover is exactly this shape: it is up,
	// it answers, and it will never stream again because it is on an abandoned
	// timeline.
	//
	// Off by default, and deliberately so: rebuilding a node can mean a
	// pg_basebackup that discards its data directory. That is not something to
	// start underneath an operator who has not asked for it, which is the same
	// reason `enabled` is off.
	//
	// The write role is never moved. A rebuilt node returns as a replica.
	AutoRejoin bool `json:"auto_rejoin,omitzero" yaml:"auto_rejoin"`

	// AutoRejoinInterval is the minimum gap between two attempts on one node,
	// so a node that cannot be rebuilt is not rebuilt continuously. Zero takes
	// the default.
	AutoRejoinInterval time.Duration `json:"auto_rejoin_interval,omitzero" yaml:"auto_rejoin_interval"`

	// AutoRejoinTimeout bounds a single attempt. Generous by default because a
	// rebuild can mean a base backup of the whole cluster. Zero takes the
	// default.
	AutoRejoinTimeout time.Duration `json:"auto_rejoin_timeout,omitzero" yaml:"auto_rejoin_timeout"`

	// AutoRejoinMaxAttempts is how many times one node is rebuilt before it is
	// left to an operator.
	//
	// A bound rather than forever: a node that fails three rebuilds is not
	// going to succeed on the fourth, and retrying past that turns a broken
	// replica into a permanent base backup against a healthy primary. Zero
	// takes the default.
	AutoRejoinMaxAttempts int `json:"auto_rejoin_max_attempts,omitzero" yaml:"auto_rejoin_max_attempts"`
}

// Defaults for the failover block. Every one of these is a documented tunable
// rather than a literal buried at a call site.
const (
	DefaultFailureThreshold     = 3
	DefaultFollowPrimaryTimeout = 30 * time.Minute
	DefaultMaxReplicaLag        = 10 * time.Second
	DefaultAutoReattachInterval = time.Minute

	// DefaultAutoRejoinInterval is long because a rebuild is expensive and a
	// node that just failed one is unlikely to pass a retry seconds later.
	DefaultAutoRejoinInterval = 5 * time.Minute

	// DefaultAutoRejoinTimeout has to cover a pg_basebackup of the whole
	// cluster, which is the slow path this feature exists to run.
	DefaultAutoRejoinTimeout = 30 * time.Minute

	// DefaultAutoRejoinMaxAttempts stops before a broken node becomes a
	// permanent load on a healthy primary.
	DefaultAutoRejoinMaxAttempts = 3
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
	if out.AutoReattach == nil {
		out.AutoReattach = new(true)
	}
	if out.AutoRejoinInterval <= 0 {
		out.AutoRejoinInterval = DefaultAutoRejoinInterval
	}
	if out.AutoRejoinTimeout <= 0 {
		out.AutoRejoinTimeout = DefaultAutoRejoinTimeout
	}
	if out.AutoRejoinMaxAttempts <= 0 {
		out.AutoRejoinMaxAttempts = DefaultAutoRejoinMaxAttempts
	}
	return out
}

// FailoverOptions returns the failover block with defaults applied, so callers
// never have to decide what a zero means.
func (c *Options) FailoverOptions() Failover {
	return c.Failover.withDefaults()
}

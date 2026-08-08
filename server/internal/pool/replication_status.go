package pool

import "time"

// replicationStatusQuery reads role and replication health in one round trip.
//
// Four values because no single one of them is trustworthy alone — see lag()
// for why. Kept as one statement so the answers describe the same instant;
// asking separately lets a node change role between the two.
const replicationStatusQuery = `SELECT
	pg_is_in_recovery(),
	COALESCE(EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp())), 0),
	COALESCE(pg_last_wal_receive_lsn() IS NOT DISTINCT FROM pg_last_wal_replay_lsn(), false),
	EXISTS (SELECT 1 FROM pg_stat_wal_receiver)`

// replicationStatus is one observation of a backend's role and how far behind
// it is.
type replicationStatus struct {
	// inRecovery is pg_is_in_recovery(): true on a replica.
	inRecovery bool

	// replayAge is now() - pg_last_xact_replay_timestamp().
	replayAge time.Duration

	// caughtUp is whether everything received has been replayed.
	caughtUp bool

	// streaming is whether a WAL receiver is currently connected.
	streaming bool
}

func (s replicationStatus) role() Role {
	if s.inRecovery {
		return RoleReplica
	}
	return RolePrimary
}

// lag reports how stale this backend's data is.
//
// Neither obvious signal works on its own, and both failures are silent:
//
//   - Time since last replay alone reports a healthy replica as badly lagged
//     whenever the primary is simply idle. Nothing has been written, so nothing
//     has been replayed, and the clock keeps running.
//   - LSN equality alone reports a replica that has been *cut off entirely* as
//     perfectly caught up. Its WAL receiver is gone, so it stops receiving,
//     finishes replaying the little it had, and receive == replay forever. That
//     node then looks like the freshest replica in the pool while serving data
//     that is hours old — the worst outcome available here, because reads
//     silently return stale rows rather than failing.
//
// So a replica is only credited with zero lag when it is *both* connected to a
// primary and has replayed everything it received. Otherwise the replay age
// stands, which is what grows for a disconnected node.
func (s replicationStatus) lag() time.Duration {
	if !s.inRecovery {
		return 0
	}
	if s.streaming && s.caughtUp {
		return 0
	}
	if s.replayAge < 0 {
		return 0 // clock skew between the proxy and the backend
	}
	return s.replayAge
}

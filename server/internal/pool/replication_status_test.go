package pool

import (
	"testing"
	"time"
)

func TestReplicationStatusLag(t *testing.T) {
	for name, tc := range map[string]struct {
		status replicationStatus
		want   time.Duration
		why    string
	}{
		"primary": {
			status: replicationStatus{inRecovery: false, replayAge: time.Hour},
			want:   0,
			why:    "a primary has no replication lag, whatever the replay timestamp says",
		},
		"idle primary upstream": {
			status: replicationStatus{inRecovery: true, replayAge: 10 * time.Minute, caughtUp: true, streaming: true},
			want:   0,
			why: "connected and fully replayed: the replay age is only large because " +
				"nothing has been written upstream",
		},
		"genuinely behind": {
			status: replicationStatus{inRecovery: true, replayAge: 30 * time.Second, caughtUp: false, streaming: true},
			want:   30 * time.Second,
			why:    "receiving WAL faster than it can replay it",
		},
		"cut off from the primary": {
			status: replicationStatus{inRecovery: true, replayAge: 2 * time.Hour, caughtUp: true, streaming: false},
			want:   2 * time.Hour,
			why: "no WAL receiver, so receive==replay forever; LSN equality alone would " +
				"call this the freshest replica in the pool while it serves two-hour-old rows",
		},
		"clock skew": {
			status: replicationStatus{inRecovery: true, replayAge: -5 * time.Second, streaming: true},
			want:   0,
			why:    "a negative age means the clocks disagree, not that the replica is ahead",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.status.lag(); got != tc.want {
				t.Errorf("lag() = %v, want %v — %s", got, tc.want, tc.why)
			}
		})
	}
}

func TestReplicationStatusRole(t *testing.T) {
	if got := (replicationStatus{inRecovery: true}).role(); got != RoleReplica {
		t.Errorf("in recovery => %v, want replica", got)
	}
	if got := (replicationStatus{inRecovery: false}).role(); got != RolePrimary {
		t.Errorf("not in recovery => %v, want primary", got)
	}
}

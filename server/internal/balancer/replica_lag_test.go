package balancer

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// When every replica is behind, reads must still be served rather than
// failing: a stale answer beats no answer, and the primary is the alternative
// only if the caller can take the write-path load.
func TestAllReplicasLaggingStillYieldsATarget(t *testing.T) {
	t.Cleanup(func() { SetMaxReplicaLag(DefaultMaxReplicaLag) })

	behind := &mockBackend{address: "r1", healthy: true, role: pool.RoleReplica, lag: time.Minute}
	alsoBehind := &mockBackend{address: "r2", healthy: true, role: pool.RoleReplica, lag: time.Minute}
	nodes := []pool.Backend{behind, alsoBehind}

	SetMaxReplicaLag(time.Second)

	targets := FilterNodes(nodes, Hint{ReadOnly: true})
	defer targetsPool.Put(targets)

	if len(*targets) == 0 {
		t.Error("no read target when every replica is behind; reads would fail outright")
	}
}

// With a fresh replica available, a lagging one must not be chosen.
func TestLaggingReplicaIsExcludedWhenAFreshOneExists(t *testing.T) {
	t.Cleanup(func() { SetMaxReplicaLag(DefaultMaxReplicaLag) })

	fresh := &mockBackend{address: "r-fresh", healthy: true, role: pool.RoleReplica, lag: 100 * time.Millisecond}
	stale := &mockBackend{address: "r-stale", healthy: true, role: pool.RoleReplica, lag: 30 * time.Second}
	nodes := []pool.Backend{fresh, stale}

	SetMaxReplicaLag(time.Second)

	targets := FilterNodes(nodes, Hint{ReadOnly: true})
	defer targetsPool.Put(targets)

	if len(*targets) != 1 || (*targets)[0].Address() != "r-fresh" {
		var got []string
		for _, n := range *targets {
			got = append(got, n.Address())
		}
		t.Errorf("read targets = %v, want only r-fresh; a replica 30s behind was eligible", got)
	}
}

// Raising the threshold must make a previously excluded replica eligible,
// without a restart.
func TestRaisingTheThresholdAdmitsALaggingReplica(t *testing.T) {
	t.Cleanup(func() { SetMaxReplicaLag(DefaultMaxReplicaLag) })

	fresh := &mockBackend{address: "r-fresh", healthy: true, role: pool.RoleReplica, lag: 0}
	behind := &mockBackend{address: "r-behind", healthy: true, role: pool.RoleReplica, lag: 5 * time.Second}
	nodes := []pool.Backend{fresh, behind}

	SetMaxReplicaLag(time.Second)
	targets := FilterNodes(nodes, Hint{ReadOnly: true})
	if len(*targets) != 1 {
		t.Fatalf("expected only the fresh replica, got %d", len(*targets))
	}
	targetsPool.Put(targets)

	SetMaxReplicaLag(time.Minute)
	targets = FilterNodes(nodes, Hint{ReadOnly: true})
	defer targetsPool.Put(targets)
	if len(*targets) != 2 {
		t.Errorf("expected both replicas after raising the threshold, got %d", len(*targets))
	}
}

// A zero or negative threshold must restore the default, not disable the gate.
// Routing reads to a replica an unbounded distance behind is never the intent.
func TestZeroThresholdRestoresTheDefault(t *testing.T) {
	t.Cleanup(func() { SetMaxReplicaLag(DefaultMaxReplicaLag) })

	SetMaxReplicaLag(0)
	if got := MaxReplicaLag(); got != DefaultMaxReplicaLag {
		t.Errorf("MaxReplicaLag() = %v after setting 0, want the default %v", got, DefaultMaxReplicaLag)
	}

	SetMaxReplicaLag(-time.Second)
	if got := MaxReplicaLag(); got != DefaultMaxReplicaLag {
		t.Errorf("MaxReplicaLag() = %v after a negative value, want the default", got)
	}
}

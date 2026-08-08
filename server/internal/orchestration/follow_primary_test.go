package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// After a promotion the other replicas are still streaming from the node that
// died. Nothing else in the system notices — they are up, they answer queries,
// and the rows they return get quietly older.
func TestFollowPrimaryRepointsSurvivingReplicas(t *testing.T) {
	dead := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	promoted := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	survivor := &mockBackend{address: "r2", role: pool.RoleReplica, healthy: true}
	other := &mockBackend{address: "r3", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{dead, promoted, survivor, other}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, FailureThreshold: 1, FollowPrimary: true})

	mgr.followNewPrimary(context.Background(), "r1", 5*time.Second)

	provisioner.mu.Lock()
	defer provisioner.mu.Unlock()

	for _, addr := range []string{"r2", "r3"} {
		if provisioner.repointed[addr] != "r1" {
			t.Errorf("replica %s follows %q, want r1; it is still streaming from the dead primary",
				addr, provisioner.repointed[addr])
		}
	}

	// The old primary has diverged onto an abandoned timeline. Re-pointing it
	// is not a re-point, it is a rebuild, and that is an operator's call.
	if _, ok := provisioner.repointed["p1"]; ok {
		t.Error("the failed primary was re-pointed; it needs pg_rewind or a rebuild, not SetupReplication")
	}
	if _, ok := provisioner.repointed["r1"]; ok {
		t.Error("the newly promoted primary was told to follow itself")
	}
}

// A replica that is itself down cannot be re-pointed — dialling its agent and
// starting a base backup against a dead host just burns the timeout.
func TestFollowPrimarySkipsDownReplicas(t *testing.T) {
	promoted := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	down := &mockBackend{address: "r2", role: pool.RoleReplica, healthy: false}

	backends := []pool.Backend{promoted, down}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, FollowPrimary: true})

	mgr.followNewPrimary(context.Background(), "r1", 5*time.Second)

	provisioner.mu.Lock()
	defer provisioner.mu.Unlock()
	if len(provisioner.repointed) != 0 {
		t.Errorf("a down replica was re-pointed anyway: %v", provisioner.repointed)
	}
}

// One replica failing to re-point must not stop the others.
func TestFollowPrimaryContinuesPastAFailure(t *testing.T) {
	promoted := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	broken := &mockBackend{address: "r2", role: pool.RoleReplica, healthy: true}
	fine := &mockBackend{address: "r3", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{promoted, broken, fine}
	provisioner := &mockProvisioner{failDemoteFor: "r2"}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, FollowPrimary: true})

	mgr.followNewPrimary(context.Background(), "r1", 5*time.Second)

	provisioner.mu.Lock()
	defer provisioner.mu.Unlock()
	if provisioner.repointed["r3"] != "r1" {
		t.Error("r3 was not re-pointed after r2 failed; one bad node stopped the rest")
	}
}

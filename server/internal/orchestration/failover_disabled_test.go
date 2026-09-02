package orchestration

import (
	"testing"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// failover.enabled: false has to mean it, whoever asks.
//
// The gate lived in the health monitor and nowhere else, while the data plane
// calls TriggerFailover directly the moment a write cannot find a primary. So a
// deployment that had deliberately asked for a human in the loop — the only
// sane setting when promotion starts a new timeline and cannot be undone — got
// a replica promoted by a write arriving during a blip.
//
// It stayed invisible because promotion itself was broken: pg_ctl refused to
// run as the root the agent runs as, so every attempt failed. Fixing promotion
// is what made this bite, and it promoted a live test cluster's replica while
// that cluster's config said failover was off.
func TestDisabledFailoverPromotesNothing(t *testing.T) {
	provisioner := &mockProvisioner{}
	primary := &mockBackend{address: "primary:5432", role: pool.RolePrimary, healthy: false}
	replica := &mockBackend{address: "replica:5432", role: pool.RoleReplica, healthy: true}

	mgr := NewFailoverManager(provisioner, nil,
		func() []pool.Backend { return []pool.Backend{primary, replica} },
		Options{Enabled: false, FailureThreshold: 1})

	if err := mgr.TriggerFailover(t.Context()); err != nil {
		t.Fatalf("TriggerFailover returned %v; declining is not a failure", err)
	}

	if got := promotedBy(provisioner); got != "" {
		t.Errorf("promoted %s with automatic failover disabled", got)
	}
}

// And still promotes when it is enabled, so the gate cannot be satisfied by
// never promoting at all.
func TestEnabledFailoverStillPromotes(t *testing.T) {
	provisioner := &mockProvisioner{}
	primary := &mockBackend{address: "primary:5432", role: pool.RolePrimary, healthy: false}
	replica := &mockBackend{address: "replica:5432", role: pool.RoleReplica, healthy: true}

	mgr := NewFailoverManager(provisioner, nil,
		func() []pool.Backend { return []pool.Backend{primary, replica} },
		immediateFailover())

	if err := mgr.TriggerFailover(t.Context()); err != nil {
		t.Fatalf("TriggerFailover with failover enabled returned %v", err)
	}
	if got := promotedBy(provisioner); got != "replica:5432" {
		t.Errorf("promoted %q, want replica:5432", got)
	}
}

// promotedBy reads the mock's record under its lock, which the production type
// also needs — verifyPromotion reads a role the monitor may be writing.
func promotedBy(p *mockProvisioner) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.promoted
}

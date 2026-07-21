package orchestration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

type mockBackend struct {
	pool.Backend
	address string
	role    pool.Role
	healthy bool
}

func (m *mockBackend) Address() string   { return m.address }
func (m *mockBackend) Role() pool.Role   { return m.role }
func (m *mockBackend) IsHealthy() bool   { return m.healthy }
func (m *mockBackend) SetHealthy(h bool) { m.healthy = h }
func (m *mockBackend) ReevaluateRole()   {}

type mockProvisioner struct {
	Provisioner
	promoted string
	demoted  []string
	lag      time.Duration
	mu       sync.Mutex
}

func (m *mockProvisioner) PromoteToPrimary(ctx context.Context, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promoted = addr
	return nil
}

func (m *mockProvisioner) DemoteToReplica(ctx context.Context, addr, primary string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.demoted = append(m.demoted, addr)
	return nil
}

func (m *mockProvisioner) CheckReplicationLag(ctx context.Context, addr string) (time.Duration, error) {
	return m.lag, nil
}

type mockConsensus struct {
	Consensus
	leader bool
}

func (m *mockConsensus) IsLeader() bool                  { return m.leader }
func (m *mockConsensus) GetPrimary() (string, error)     { return "p1", nil }
func (m *mockConsensus) SetPrimary(address string) error { return nil }

func TestFailoverManager_AutomaticFailover(t *testing.T) {
	p1 := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	r1 := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{p1, r1}
	provisioner := &mockProvisioner{}
	consensus := &mockConsensus{leader: true}

	mgr := NewFailoverManager(provisioner, consensus, func() []pool.Backend { return backends })

	// Manually call monitor instead of Start to avoid non-determinism in test
	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	if provisioner.promoted != "r1" {
		t.Errorf("Expected r1 to be promoted, got %s", provisioner.promoted)
	}
	provisioner.mu.Unlock()
}

func TestFailoverManager_AutomaticFailback_SplitBrain(t *testing.T) {
	p1 := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: true}
	p2 := &mockBackend{address: "p2", role: pool.RolePrimary, healthy: true}

	backends := []pool.Backend{p1, p2}
	provisioner := &mockProvisioner{}
	consensus := &mockConsensus{leader: true}

	mgr := NewFailoverManager(provisioner, consensus, func() []pool.Backend { return backends })

	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	if len(provisioner.demoted) != 1 {
		t.Errorf("Expected 1 backend to be demoted, got %d", len(provisioner.demoted))
	} else if provisioner.demoted[0] != "p2" {
		t.Errorf("Expected p2 to be demoted, got %s", provisioner.demoted[0])
	}
	provisioner.mu.Unlock()
}

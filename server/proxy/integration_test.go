package proxy

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service"
	"github.com/gsoultan/pontus/pkg/config"
	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/orchestration"
	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

type mockIntegrationBackend struct {
	addr    string
	role    pool.Role
	healthy atomic.Bool
	mu      sync.Mutex
}

func (m *mockIntegrationBackend) Acquire(ctx context.Context) (net.Conn, error) {
	if !m.healthy.Load() {
		return nil, errors.New("backend unhealthy")
	}
	return &mockConn{data: []byte("OK")}, nil
}

// AcquireFor ignores the identity: the double does not pool.
func (m *mockIntegrationBackend) AcquireFor(ctx context.Context, _, _ string) (net.Conn, error) {
	return m.Acquire(ctx)
}

func (m *mockIntegrationBackend) Release(conn net.Conn) error { return nil }
func (m *mockIntegrationBackend) IsHealthy() bool             { return m.healthy.Load() }
func (m *mockIntegrationBackend) Address() string             { return m.addr }
func (m *mockIntegrationBackend) Zone() string                { return "zone1" }
func (m *mockIntegrationBackend) ErrorRate() float64          { return 0 }
func (m *mockIntegrationBackend) ActiveConns() int64          { return 0 }
func (m *mockIntegrationBackend) Role() pool.Role {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.role
}
func (m *mockIntegrationBackend) SetRole(r pool.Role) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.role = r
}
func (m *mockIntegrationBackend) SetWeight(int)                           {}
func (m *mockIntegrationBackend) Weight() int                             { return 1 }
func (m *mockIntegrationBackend) Latency() time.Duration                  { return time.Millisecond }
func (m *mockIntegrationBackend) RTT() time.Duration                      { return time.Millisecond }
func (m *mockIntegrationBackend) ReportLatency(time.Duration)             {}
func (m *mockIntegrationBackend) ReportRTT(time.Duration)                 {}
func (m *mockIntegrationBackend) ReplicationLag() time.Duration           { return 0 }
func (m *mockIntegrationBackend) IsReplicating() bool                     { return true }
func (m *mockIntegrationBackend) LastHealthy() time.Time                  { return time.Now() }
func (m *mockIntegrationBackend) IsDraining() bool                        { return false }
func (m *mockIntegrationBackend) SetDraining(bool)                        {}
func (m *mockIntegrationBackend) ReevaluateRole()                         {}
func (m *mockIntegrationBackend) IncRequests()                            {}
func (m *mockIntegrationBackend) IncErrors()                              {}
func (m *mockIntegrationBackend) Stats() pool.BackendStats                { return pool.BackendStats{} }
func (m *mockIntegrationBackend) SetHealthy(h bool)                       { m.healthy.Store(h) }
func (m *mockIntegrationBackend) ReportResult(error)                      {}
func (m *mockIntegrationBackend) AgentClient() service.AgentServiceClient { return nil }
func (m *mockIntegrationBackend) AgentToken() string                      { return "" }
func (m *mockIntegrationBackend) AgentAddr() string                       { return "" }
func (m *mockIntegrationBackend) Close() error                            { return nil }
func (m *mockIntegrationBackend) InstalledVersion() string                { return "" }
func (m *mockIntegrationBackend) RecommendedVersion() string              { return "" }
func (m *mockIntegrationBackend) AvailableVersions() []string             { return nil }
func (m *mockIntegrationBackend) DatabaseMetrics() *domain.DatabaseMetrics {
	return nil
}

type mockIntegrationHandler struct {
	mockHandler
}

func (m *mockIntegrationHandler) GetCurrentLSN(ctx context.Context, conn net.Conn) (string, error) {
	return "0/1000", nil
}
func (m *mockIntegrationHandler) WaitLSN(ctx context.Context, conn net.Conn, lsn string) error {
	return nil
}

type mockProvisioner struct {
	backends []pool.Backend
}

func (p *mockProvisioner) PromoteToPrimary(ctx context.Context, addr string) error {
	for _, b := range p.backends {
		if b.Address() == addr {
			b.(*mockIntegrationBackend).SetRole(pool.RolePrimary)
		} else if b.Role() == pool.RolePrimary {
			b.(*mockIntegrationBackend).SetRole(pool.RoleReplica)
		}
	}
	return nil
}

func (p *mockProvisioner) DemoteToReplica(ctx context.Context, addr string, primaryAddr string) error {
	for _, b := range p.backends {
		if b.Address() == addr {
			b.(*mockIntegrationBackend).SetRole(pool.RoleReplica)
		}
	}
	return nil
}

func (p *mockProvisioner) CheckReplicationLag(ctx context.Context, addr string) (time.Duration, error) {
	return 0, nil
}

func (p *mockProvisioner) ProvisionReplica(ctx context.Context, req *endpoints.ProvisionReplicaRequest, progress chan<- *endpoints.ProvisionProgress) error {
	return nil
}

type mockConsensus struct {
	orchestration.Consensus
}

func (m *mockConsensus) IsLeader() bool                  { return true }
func (m *mockConsensus) GetPrimary() (string, error)     { return "primary:5432", nil }
func (m *mockConsensus) SetPrimary(address string) error { return nil }

func TestFailoverIntegration(t *testing.T) {
	// 1. Setup
	b1 := &mockIntegrationBackend{addr: "primary:5432", role: pool.RolePrimary}
	b1.SetHealthy(true)
	b2 := &mockIntegrationBackend{addr: "replica:5432", role: pool.RoleReplica}
	b2.SetHealthy(true)

	backends := []pool.Backend{b1, b2}
	lb := balancer2.NewRoundRobin(backends)
	handler := &mockIntegrationHandler{}
	handler.state = protocol.StateIdle

	prov := &mockProvisioner{backends: backends}
	consensus := &mockConsensus{}
	fm := orchestration.NewFailoverManager(prov, consensus,
		func() []pool.Backend { return backends },
		orchestration.Options{Enabled: true, FailureThreshold: 1})

	gw := NewGateway(handler, lb, fm, &config.Options{}, nil)

	// 2. Initial check - should work
	ctx := t.Context()
	b, conn, err := gw.acquireBackend(ctx, balancer2.Hint{ReadOnly: false})
	if err != nil {
		t.Fatalf("Failed to acquire backend initially: %v", err)
	}
	if b.Address() != "primary:5432" {
		t.Errorf("Expected primary, got %s", b.Address())
	}
	conn.Close()

	// 3. Simulate Primary failure
	b1.SetHealthy(false)
	lb.UpdateNodes(backends)

	// 4. Try to acquire - should trigger failover and eventually succeed with new primary
	resChan := make(chan struct {
		b   pool.Backend
		err error
	}, 1)

	go func() {
		b, _, err := gw.acquireBackend(ctx, balancer2.Hint{ReadOnly: false})
		resChan <- struct {
			b   pool.Backend
			err error
		}{b, err}
	}()

	// Start FailoverManager
	go fm.Start(ctx)

	// To speed up test, manually trigger one check
	fm.TriggerFailover(ctx)

	// 5. Verify result
	select {
	case res := <-resChan:
		if res.err != nil {
			t.Fatalf("Failed to acquire backend after failover: %v", res.err)
		}
		if res.b.Address() != "replica:5432" {
			t.Errorf("Expected newly promoted primary (replica:5432), got %s", res.b.Address())
		}
		if res.b.Role() != pool.RolePrimary {
			t.Errorf("Backend role should be Primary, got %v", res.b.Role())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out waiting for failover resolution")
	}
}

// Admin reports no administrative channel: the failover path under test does
// not use one.
func (m *mockIntegrationBackend) Admin() *pool.AdminSession { return nil }

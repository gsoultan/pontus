package balancer

import (
	"context"
	"net"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

type mockBackend struct {
	pool.Backend
	address     string
	healthy     bool
	activeConns int64
	role        pool.Role
	weight      int
	latency     time.Duration
	lastHealthy time.Time
}

func (m *mockBackend) Address() string    { return m.address }
func (m *mockBackend) Zone() string       { return "" }
func (m *mockBackend) ErrorRate() float64 { return 0 }
func (m *mockBackend) IsHealthy() bool    { return m.healthy }
func (m *mockBackend) ActiveConns() int64 { return m.activeConns }
func (m *mockBackend) Role() pool.Role {
	if m.role == "" {
		return pool.RolePrimary
	}
	return m.role
}
func (m *mockBackend) Weight() int {
	if m.weight <= 0 {
		return 1
	}
	return m.weight
}
func (m *mockBackend) Latency() time.Duration                        { return m.latency }
func (m *mockBackend) RTT() time.Duration                            { return 0 }
func (m *mockBackend) ReportLatency(time.Duration)                   {}
func (m *mockBackend) ReportRTT(time.Duration)                       {}
func (m *mockBackend) ReplicationLag() time.Duration                 { return 0 }
func (m *mockBackend) LastHealthy() time.Time                        { return m.lastHealthy }
func (m *mockBackend) IsDraining() bool                              { return false }
func (m *mockBackend) SetDraining(bool)                              {}
func (m *mockBackend) ReevaluateRole()                               {}
func (m *mockBackend) SetHealthy(h bool)                             { m.healthy = h }
func (m *mockBackend) ReportResult(error)                            {}
func (m *mockBackend) Close() error                                  { return nil }
func (m *mockBackend) Acquire(ctx context.Context) (net.Conn, error) { return nil, nil }
func (m *mockBackend) Release(conn net.Conn) error                   { return nil }
func (m *mockBackend) IncRequests()                                  {}
func (m *mockBackend) IncErrors()                                    {}

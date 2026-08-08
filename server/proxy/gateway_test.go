package proxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/service"
	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/pool"
	protocol2 "github.com/gsoultan/pontus/server/internal/protocol"
)

type mockHandler struct {
	state protocol2.TransactionState
}

func (m *mockHandler) Handshake(ctx context.Context, client, server net.Conn, state *protocol2.SessionState) error {
	return nil
}

func (m *mockHandler) PeekTransactionState(data []byte) (protocol2.TransactionState, error) {
	return m.state, nil
}

func (m *mockHandler) Identify() protocol2.Metadata {
	return protocol2.Metadata{Name: "Mock"}
}

func (m *mockHandler) Execute(ctx context.Context, conn net.Conn, query string) error {
	return nil
}

func (m *mockHandler) ClassifyQuery(data []byte) protocol2.QueryInfo {
	return protocol2.QueryInfo{ReadOnly: false}
}

func (m *mockHandler) TrackSessionState(state *protocol2.SessionState, data []byte) {}

func (m *mockHandler) NormalizeQuery(data []byte) string { return string(data) }

func (m *mockHandler) TrackPreparedStatement(state *protocol2.SessionState, data []byte) {}

func (m *mockHandler) ReplayPreparedStatements(ctx context.Context, conn net.Conn, state *protocol2.SessionState) error {
	return nil
}

func (m *mockHandler) ReplaySessionState(ctx context.Context, conn net.Conn, state *protocol2.SessionState) error {
	return nil
}

func (m *mockHandler) DeepCheck(ctx context.Context, conn net.Conn) error {
	return nil
}

func (m *mockHandler) IsReadOnly(ctx context.Context, conn net.Conn) (bool, error) {
	return false, nil
}

func (m *mockHandler) GetReplicationLag(ctx context.Context, conn net.Conn) (time.Duration, error) {
	return 0, nil
}

func (m *mockHandler) IsReadOnlyError(data []byte) bool {
	return false
}

func (m *mockHandler) DiscoverTopology(ctx context.Context, conn net.Conn) ([]string, error) {
	return nil, nil
}
func (m *mockHandler) GetCurrentLSN(ctx context.Context, conn net.Conn) (string, error) {
	return "0/1000", nil
}
func (m *mockHandler) WaitLSN(ctx context.Context, conn net.Conn, lsn string) error {
	return nil
}

func (m *mockHandler) CollectMetrics(ctx context.Context, conn net.Conn) (*domain.DatabaseMetrics, error) {
	return &domain.DatabaseMetrics{}, nil
}

func (m *mockHandler) IsPinned(state *protocol2.SessionState) bool {
	return false
}

type mockBackend struct {
	released chan bool
}

func (m *mockBackend) Acquire(ctx context.Context) (net.Conn, error) {
	return &mockConn{data: []byte("response")}, nil
}

func (m *mockBackend) Release(conn net.Conn) error {
	m.released <- true
	return nil
}

func (m *mockBackend) IsHealthy() bool               { return true }
func (m *mockBackend) Address() string               { return "mock:5432" }
func (m *mockBackend) Zone() string                  { return "" }
func (m *mockBackend) ErrorRate() float64            { return 0 }
func (m *mockBackend) ActiveConns() int64            { return 0 }
func (m *mockBackend) Role() pool.Role               { return pool.RolePrimary }
func (m *mockBackend) Weight() int                   { return 1 }
func (m *mockBackend) Latency() time.Duration        { return 0 }
func (m *mockBackend) RTT() time.Duration            { return 0 }
func (m *mockBackend) ReportLatency(time.Duration)   {}
func (m *mockBackend) ReportRTT(time.Duration)       {}
func (m *mockBackend) ReplicationLag() time.Duration { return 0 }
func (m *mockBackend) IsReplicating() bool           { return true }
func (m *mockBackend) LastHealthy() time.Time        { return time.Time{} }
func (m *mockBackend) IsDraining() bool              { return false }
func (m *mockBackend) SetDraining(bool)              {}
func (m *mockBackend) ReevaluateRole()               {}
func (m *mockBackend) IncRequests()                  {}
func (m *mockBackend) IncErrors()                    {}
func (m *mockBackend) Stats() pool.BackendStats {
	return pool.BackendStats{}
}
func (m *mockBackend) SetWeight(int)                           {}
func (m *mockBackend) SetHealthy(bool)                         {}
func (m *mockBackend) ReportResult(error)                      {}
func (m *mockBackend) AgentClient() service.AgentServiceClient { return nil }
func (m *mockBackend) AgentToken() string                      { return "" }
func (m *mockBackend) Close() error                            { return nil }
func (m *mockBackend) InstalledVersion() string                { return "" }
func (m *mockBackend) RecommendedVersion() string              { return "" }
func (m *mockBackend) AvailableVersions() []string             { return nil }
func (m *mockBackend) DatabaseMetrics() *domain.DatabaseMetrics {
	return nil
}

type mockBalancer struct {
	backend *mockBackend
}

func (m *mockBalancer) Next(ctx context.Context, hint balancer.Hint) (pool.Backend, error) {
	return m.backend, nil
}

func (m *mockBalancer) UpdateNodes(nodes []pool.Backend) {}

func (m *mockBalancer) Name() string { return "Mock" }

type mockConn struct {
	net.Conn
	data []byte
	read bool
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.read {
		return 0, net.ErrClosed
	}
	n = copy(b, m.data)
	m.read = true
	return n, nil
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return len(b), nil
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
}

func (m *mockConn) Close() error                  { return nil }
func (m *mockConn) SetDeadline(t time.Time) error { return nil }

func TestGateway_HandleClient_TransactionRelease(t *testing.T) {
	handler := &mockHandler{state: protocol2.StateIdle}
	backend := &mockBackend{released: make(chan bool, 10)}
	lb := &mockBalancer{backend: backend}
	gateway := NewGateway(handler, lb, nil, &config.Options{}, nil)

	client := &mockConn{data: []byte("query")}

	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()

	// Run handleClient in a goroutine because it has a loop
	go gateway.handleClient(ctx, client)

	select {
	case <-backend.released:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Connection was not released after idle transaction")
	}
}

func (m *mockHandler) StartReplication(_ context.Context, _, _ net.Conn, _ *protocol2.SessionState) error {
	return nil
}

// Admin reports no administrative channel: these tests exercise the pooled
// path, which does not use one.
func (m *mockBackend) Admin() *pool.AdminSession { return nil }

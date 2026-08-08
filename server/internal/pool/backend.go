package pool

import (
	"context"
	"net"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/service"
)

// Role represents the backend server role (Primary or Replica).
type Role string

const (
	RolePrimary Role = "primary"
	RoleReplica Role = "replica"
)

// Backend represents a database server that can accept connections.
type Backend interface {
	// Acquire returns an active connection to the backend.
	Acquire(ctx context.Context) (net.Conn, error)

	// Release returns a connection to the pool.
	Release(conn net.Conn) error

	// IsHealthy checks the current health status of the backend.
	IsHealthy() bool

	// SetHealthy updates the health status.
	SetHealthy(bool)

	// Address returns the network address of the backend.
	Address() string

	// Admin returns Pontus's own authenticated channel to this backend, or nil
	// when no admin_dsn is configured. Client sessions carry the client's
	// credentials, so administrative statements need a session of their own.
	Admin() *AdminSession
	// Zone returns the availability zone of the backend.
	Zone() string
	// ActiveConns returns the number of active connections.
	ActiveConns() int64

	// Role returns the backend role.
	Role() Role

	// Weight returns the configured weight of the backend.
	Weight() int

	// SetWeight updates the backend weight.
	SetWeight(int)

	// Latency returns the current estimated latency of the backend.
	Latency() time.Duration
	// RTT returns the current estimated TCP Round Trip Time.
	RTT() time.Duration
	// ErrorRate returns the current estimated error rate (0.0 to 1.0).
	ErrorRate() float64
	// ReportLatency updates the latency estimate.
	ReportLatency(d time.Duration)
	// ReportRTT updates the RTT estimate.
	ReportRTT(d time.Duration)

	// ReplicationLag returns the current replication lag.
	ReplicationLag() time.Duration

	// IsReplicating reports whether the backend is fit to serve reads. A
	// primary always is; a replica only while it has a WAL receiver attached.
	IsReplicating() bool

	// LastHealthy returns the time when the backend last became healthy.
	LastHealthy() time.Time

	// IsDraining returns true if the backend is currently draining connections.
	IsDraining() bool

	// SetDraining updates the draining status.
	SetDraining(bool)

	// Stats returns connection pool statistics.
	Stats() BackendStats

	// DatabaseMetrics returns the last collected database-specific metrics.
	DatabaseMetrics() *domain.DatabaseMetrics

	// InstalledVersion returns the current installed database version.
	InstalledVersion() string
	// RecommendedVersion returns the recommended database version.
	RecommendedVersion() string
	// AvailableVersions returns all available database versions.
	AvailableVersions() []string

	// IncRequests increments the total requests counter.
	IncRequests()

	// IncErrors increments the total errors counter.
	IncErrors()

	// ReevaluateRole triggers an immediate check of the backend role and health.
	ReevaluateRole()

	// ReportResult informs the backend about the result of a request to adjust capacity.
	ReportResult(err error)

	// AgentClient returns the gRPC client for the backend's agent.
	AgentClient() service.AgentServiceClient

	// AgentToken returns the authentication token for the backend's agent.
	AgentToken() string

	// Close shuts down the backend and closes all connections.
	Close() error
}

type BackendStats struct {
	MaxConns      int32
	ActiveConns   int32
	IdleConns     int32
	WaitQueueSize int64
	TotalRequests int64
	TotalErrors   int64
	AvgWaitDelay  time.Duration
}

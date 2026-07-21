package protocol

import (
	"context"
	"net"
	"time"
)

// HealthChecker defines protocol-specific health check operations.
type HealthChecker interface {
	// DeepCheck executes a protocol-specific health check.
	DeepCheck(ctx context.Context, conn net.Conn) error

	// IsReadOnly checks if the backend node is in read-only mode.
	IsReadOnly(ctx context.Context, conn net.Conn) (bool, error)

	// GetReplicationLag returns the current replication lag of the backend.
	GetReplicationLag(ctx context.Context, conn net.Conn) (time.Duration, error)

	// IsReadOnlyError checks if the given data contains a "read-only" error response.
	IsReadOnlyError(data []byte) bool
}

package protocol

import (
	"context"
	"net"
)

// ConsistencyManager handles replication consistency and LSN tracking.
type ConsistencyManager interface {
	// GetCurrentLSN returns the current LSN of the primary.
	GetCurrentLSN(ctx context.Context, conn net.Conn) (string, error)

	// WaitLSN blocks until the replica has caught up to the target LSN.
	WaitLSN(ctx context.Context, conn net.Conn, targetLSN string) error
}

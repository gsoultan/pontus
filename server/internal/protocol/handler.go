package protocol

import (
	"context"
	"net"
)

// Handler defines the protocol-specific logic for database communication.
type Handler interface {
	QueryClassifier
	SessionTracker
	HealthChecker
	TopologyDiscoverer
	ConsistencyManager
	MetricsCollector

	// Handshake manages the initial auth and startup sequence between client and server.
	// StartReplication completes the startup exchange for a replication stream
	// on a connection the caller has already chosen. The node is not
	// interchangeable: a slot lives on exactly one backend.
	StartReplication(ctx context.Context, client, server net.Conn, state *SessionState) error

	Handshake(ctx context.Context, client, server net.Conn, state *SessionState) error

	// PeekTransactionState inspects data to track transaction boundaries.
	PeekTransactionState(data []byte) (TransactionState, error)

	// Identify returns the protocol metadata.
	Identify() Metadata

	// Execute executes a simple query and waits for completion.
	Execute(ctx context.Context, conn net.Conn, query string) error
}

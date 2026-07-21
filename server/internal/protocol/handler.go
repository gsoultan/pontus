package protocol

import (
	"context"
	"net"

	"github.com/gsoultan/pontus/pkg/config"
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
	Handshake(ctx context.Context, client, server net.Conn, state *SessionState) error

	// PeekTransactionState inspects data to track transaction boundaries.
	PeekTransactionState(data []byte) (TransactionState, error)

	// Identify returns the protocol metadata.
	Identify() Metadata

	// Execute executes a simple query and waits for completion.
	Execute(ctx context.Context, conn net.Conn, query string) error

	// RewriteQuery allows the protocol handler to modify the query (e.g. for masking).
	RewriteQuery(data []byte, rules []config.MaskingRule) ([]byte, error)
}

package orchestration

import (
	"context"
)

// Consensus defines the interface for cluster-wide agreement on the primary node.
type Consensus interface {
	// Start starts the consensus node.
	Start(ctx context.Context) error

	// IsLeader returns true if the current node is the cluster leader.
	IsLeader() bool

	// LeaderID returns the ID of the current leader.
	LeaderID() string

	// GetPrimary returns the address of the current primary database node.
	//
	// A local read of applied state. Just after startup the log is still being
	// replayed, so this can answer *empty* rather than merely stale — and empty
	// is not "ask again", it is "there is no primary". Callers that act on it
	// must be able to wait; see WaitForApplied.
	GetPrimary() (string, error)

	// WaitForApplied blocks until this node's state reflects everything
	// committed before the call.
	WaitForApplied(ctx context.Context) error

	// SetPrimary sets the address of the current primary database node.
	SetPrimary(address string) error

	// Join adds a new node to the cluster.
	Join(nodeID, addr string) error

	// Stop stops the consensus node.
	Stop() error
}

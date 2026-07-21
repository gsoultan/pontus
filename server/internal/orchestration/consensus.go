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
	GetPrimary() (string, error)

	// SetPrimary sets the address of the current primary database node.
	SetPrimary(address string) error

	// Join adds a new node to the cluster.
	Join(nodeID, addr string) error

	// Stop stops the consensus node.
	Stop() error
}

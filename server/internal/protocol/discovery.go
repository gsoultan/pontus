package protocol

import (
	"context"
	"net"
)

// TopologyDiscoverer provides methods for discovering database cluster topology.
type TopologyDiscoverer interface {
	// DiscoverTopology returns a list of replica addresses known by the primary.
	DiscoverTopology(ctx context.Context, conn net.Conn) ([]string, error)
}

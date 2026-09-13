package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Consensus configures Raft between control planes.
//
// Off by default. One Pontus needs no agreement with anyone, and turning this on
// changes *who may promote* — a node that is not the leader stops acting on the
// cluster entirely. That is the point, and it is not a change to make by
// accident.
//
// This is the cross-host half of a pair. The orchestration lock in `pkg/listen`
// stops two Pontus processes on one host from both orchestrating, which is what
// an overlapping binary upgrade creates; Raft stops two *hosts* from both
// deciding. Both apply, and neither replaces the other.
type Consensus struct {
	// Enabled turns Raft on.
	Enabled bool `json:"enabled,omitzero" yaml:"enabled"`

	// NodeID identifies this node in the cluster. It must be unique and stable:
	// Raft records votes against it, so two nodes sharing an id, or one node
	// changing its id, breaks the guarantee that a term has one leader.
	NodeID string `json:"node_id,omitzero" yaml:"node_id"`

	// BindAddr is where this node's Raft transport listens. Peers dial it, so
	// it must be an address they can reach — not a loopback one.
	BindAddr string `json:"bind_addr,omitzero" yaml:"bind_addr"`

	// Bootstrap creates a new single-node cluster to grow from.
	//
	// Exactly one node, exactly once. Several nodes bootstrapping form several
	// clusters of one, each with its own leader, none aware of the others —
	// which is the split brain this is meant to prevent, arranged at startup.
	// A node with existing state ignores this, so leaving it set in a unit file
	// is safe after the first start.
	Bootstrap bool `json:"bootstrap,omitzero" yaml:"bootstrap"`

	// Peers are the other nodes this one should add to the cluster.
	//
	// Only the leader can add a voter, so this is the bootstrapping node's list
	// and is ignored elsewhere. Adding a voter that is already one is harmless,
	// so it is safe to leave in place across restarts.
	Peers []ConsensusPeer `json:"peers,omitzero" yaml:"peers"`

	// DataDir is where the Raft log, stable store and snapshots live. Empty
	// puts them under the global data_dir.
	//
	// This must be durable. The log holds entries this node has acknowledged
	// and the stable store holds the votes it has cast; on tmpfs, a restart
	// re-enters a term it has already voted in.
	DataDir string `json:"data_dir,omitzero" yaml:"data_dir"`
}

// ConsensusPeer is another control plane in the cluster.
type ConsensusPeer struct {
	NodeID string `json:"node_id,omitzero" yaml:"node_id"`
	Addr   string `json:"addr,omitzero" yaml:"addr"`
}

// ErrConsensusIncomplete reports a consensus block that cannot be served.
var ErrConsensusIncomplete = errors.New("consensus is enabled but incomplete")

// ConsensusEnabled reports whether Raft should run.
func (c *Options) ConsensusEnabled() bool {
	return c != nil && c.Consensus != nil && c.Consensus.Enabled
}

// Validate reports a consensus block that cannot be served as written.
//
// Checked at startup, because every one of these failures shows up later as
// "no leader" — a cluster that never elects one looks exactly like a cluster
// waiting to, and the difference is only visible in config.
func (c *Consensus) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}

	if strings.TrimSpace(c.NodeID) == "" {
		return fmt.Errorf("%w: node_id is required, and must be unique and stable "+
			"— Raft records votes against it", ErrConsensusIncomplete)
	}
	if strings.TrimSpace(c.BindAddr) == "" {
		return fmt.Errorf("%w: bind_addr is required", ErrConsensusIncomplete)
	}
	if _, _, err := net.SplitHostPort(c.BindAddr); err != nil {
		return fmt.Errorf("%w: bind_addr %q is not host:port: %w",
			ErrConsensusIncomplete, c.BindAddr, err)
	}

	seen := map[string]struct{}{c.NodeID: {}}
	for _, peer := range c.Peers {
		if strings.TrimSpace(peer.NodeID) == "" || strings.TrimSpace(peer.Addr) == "" {
			return fmt.Errorf("%w: every peer needs a node_id and an addr", ErrConsensusIncomplete)
		}
		if _, _, err := net.SplitHostPort(peer.Addr); err != nil {
			return fmt.Errorf("%w: peer %q address %q is not host:port: %w",
				ErrConsensusIncomplete, peer.NodeID, peer.Addr, err)
		}
		if _, dup := seen[peer.NodeID]; dup {
			return fmt.Errorf("%w: node_id %q appears twice; Raft records votes "+
				"against it, so two nodes sharing one can elect two leaders in a term",
				ErrConsensusIncomplete, peer.NodeID)
		}
		seen[peer.NodeID] = struct{}{}
	}
	return nil
}

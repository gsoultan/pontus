package registry

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
	pontussystem "github.com/gsoultan/pontus/pkg/system"
	consensus2 "github.com/gsoultan/pontus/server/internal/consensus"
	orchestration2 "github.com/gsoultan/pontus/server/internal/orchestration"
)

// Raft between control planes.
//
// One node per process, not one per proxy: agreement is about who decides a
// failover for this deployment, and every proxy in it shares that answer.
//
// With consensus off, the failover manager is handed nil and every node acts on
// its own — correct for a single Pontus and the default. With it on, a node
// that is not the leader stops acting entirely, which is the point.

// startConsensus builds the Raft node, or nil when consensus is off.
//
// A failure to start is fatal to consensus, not to Pontus: the proxy still
// serves queries. But it is reported loudly, because the deployment asked for
// agreement and is not getting it, and the symptom otherwise is a failover that
// silently never happens.
func startConsensus(ctx context.Context, defaults *config.Options) orchestration2.Consensus {
	if !defaults.ConsensusEnabled() {
		return nil
	}
	cfg := defaults.Consensus

	dataDir := cfg.DataDir
	if dataDir == "" {
		base, err := pontussystem.GetDatabasePath("raft", defaults.DataDir)
		if err != nil {
			slog.Error("Consensus is enabled but has nowhere to store its log; "+
				"this node will not take part in agreement", "error", err)
			return nil
		}
		dataDir = base
	}

	node, err := consensus2.NewNode(cfg.NodeID, cfg.BindAddr, filepath.Clean(dataDir), cfg.Bootstrap)
	if err != nil {
		slog.Error("Consensus is enabled but could not start; this node will not "+
			"take part in agreement and will not act on the cluster",
			"node_id", cfg.NodeID, "bind_addr", cfg.BindAddr, "error", err)
		return nil
	}

	slog.Info("Consensus started", "node_id", cfg.NodeID, "bind_addr", cfg.BindAddr,
		"bootstrap", cfg.Bootstrap, "peers", len(cfg.Peers))

	// Adding voters needs leadership, which needs an election, so this cannot
	// be done inline. Only the bootstrapping node will succeed; on every other
	// node the attempt is refused and that is correct rather than an error.
	if len(cfg.Peers) > 0 {
		go addPeers(ctx, node, cfg.Peers)
	}
	return node
}

// addPeers brings the configured peers into the cluster once this node has
// leadership to do it with.
//
// Idempotent: adding a voter that is already one is a no-op, so the peer list
// can stay in a unit file across restarts.
func addPeers(ctx context.Context, node *consensus2.Node, peers []config.ConsensusPeer) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(peerJoinTimeout)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if !node.IsLeader() {
			if time.Now().After(deadline) {
				// Not an error. Exactly one node bootstraps and adds the
				// others; the rest are added *to* the cluster and never run
				// this to completion.
				slog.Debug("Not adding peers: this node is not the leader")
				return
			}
			continue
		}

		var failed int
		for _, peer := range peers {
			if err := node.Join(peer.NodeID, peer.Addr); err != nil {
				failed++
				slog.Warn("Could not add a consensus peer", "node_id", peer.NodeID,
					"addr", peer.Addr, "error", err)
			}
		}
		if failed == 0 {
			slog.Info("Consensus peers added", "peers", len(peers))
		}
		return
	}
}

// peerJoinTimeout bounds how long a node waits to become leader before giving
// up on adding peers. Generous, because an election takes a second or two and a
// node restarted into an existing cluster may legitimately never lead.
const peerJoinTimeout = 30 * time.Second

package consensus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

type CommandType string

const (
	CmdSyncBackends CommandType = "sync_backends"
	CmdUpdateConfig CommandType = "update_config"

	// CmdSetPrimary records which node holds the write role.
	//
	// This is what the failover manager asks consensus for. Agreeing on it is
	// the whole reason to run Raft here: without it, two control planes that
	// each see no healthy primary promote two different replicas.
	CmdSetPrimary CommandType = "set_primary"
)

type Command struct {
	Op   CommandType `json:"op"`
	Data []byte      `json:"data"`
}

type clusterState struct {
	Backends []*domain.BackendConfig `json:"backends,omitzero"`
	Config   []byte                  `json:"config,omitzero"` // Encoded proxy.Config

	// Primary is the address of the node holding the write role.
	Primary string `json:"primary,omitzero"`
}

// Node represents a node in the consensus cluster.
type Node struct {
	raft *raft.Raft
	fsm  *fsm

	// transport is kept so Stop can close the listener. Without it a node
	// leaves a TCP port bound and three goroutines running for the life of the
	// process — which a test creating nodes notices first, and a restart
	// notices second.
	transport *raft.NetworkTransport

	// store is the durable log and stable store, closed by Stop so a restart in
	// the same process can reopen the file.
	store *raftboltdb.BoltStore

	id string
}

// NewNode creates and starts a new Raft node.
func NewNode(nodeID, addr, dataDir string, bootstrap bool) (*Node, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)

	// Create data directory
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	// Set up transport
	advertiseAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve addr: %w", err)
	}
	transport, err := raft.NewTCPTransport(addr, advertiseAddr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	// Set up snapshots, log store, and stable store
	snapshots, err := raft.NewFileSnapshotStore(dataDir, 2, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot store: %w", err)
	}

	// On disk, because Raft's safety depends on it.
	//
	// These held raft.NewInmemStore, described as keeping things lightweight.
	// They do not: the log holds the entries this node has acknowledged, and
	// the stable store holds `currentTerm` and `votedFor` — the two values that
	// stop a node voting twice in one term. A node that forgets them and comes
	// back can elect a second leader in a term that already has one, which is
	// exactly the split brain a control plane runs consensus to avoid. It also
	// lost every committed entry on restart, which a test now pins.
	//
	// bbolt is pure Go, so CGO_ENABLED=0 is unaffected.
	store, err := raftboltdb.NewBoltStore(filepath.Join(dataDir, "raft.db"))
	if err != nil {
		return nil, fmt.Errorf("failed to open the raft store: %w", err)
	}
	logStore, stableStore := store, store

	// Create FSM
	fsmInstance := &fsm{}

	// Instantiate Raft
	r, err := raft.NewRaft(config, fsmInstance, logStore, stableStore, snapshots, transport)
	if err != nil {
		return nil, fmt.Errorf("failed to create raft: %w", err)
	}

	if bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      config.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		// Checked. A discarded error here returns a node that can never elect a
		// leader, so every later call reports "not leader" and nothing says
		// why — the failure is at startup and the symptom is at failover.
		// ErrCantBootstrap means this node already has state, which is the
		// ordinary case on a restart: a service unit passes the same flags
		// every time, and a node with a log must rejoin rather than start over.
		if err := r.BootstrapCluster(configuration).Error(); err != nil &&
			!errors.Is(err, raft.ErrCantBootstrap) {
			_ = transport.Close()
			_ = store.Close()
			return nil, fmt.Errorf("failed to bootstrap cluster: %w", err)
		}
	}

	return &Node{raft: r, fsm: fsmInstance, transport: transport, store: store, id: nodeID}, nil
}

// Start satisfies the orchestration.Consensus interface. Raft is already
// running by the time NewNode returns, so there is nothing further to do.
func (n *Node) Start(context.Context) error { return nil }

// Stop shuts the node down and releases its listener.
func (n *Node) Stop() error {
	if n == nil {
		return nil
	}

	var errs []error
	if n.raft != nil {
		errs = append(errs, n.raft.Shutdown().Error())
	}
	if n.transport != nil {
		errs = append(errs, n.transport.Close())
	}
	// Last: bolt takes a file lock, so a restart in the same process cannot
	// reopen the store until this one lets go of it.
	if n.store != nil {
		errs = append(errs, n.store.Close())
		n.store = nil
	}
	return errors.Join(errs...)
}

// LeaderID is the ID of the node currently holding leadership, or empty when
// there is none.
func (n *Node) LeaderID() string {
	_, id := n.raft.LeaderWithID()
	return string(id)
}

// WaitForApplied blocks until this node's FSM reflects everything committed
// before the call.
//
// Reads here are local: GetPrimary and GetConfig answer from applied state,
// which is the ordinary Raft trade — cheap reads, possibly a little stale. On a
// node that has just started that trade is different in kind, because the log
// is replayed into the FSM asynchronously and leadership can arrive first. The
// answer is then not stale but *empty*, and to the failover manager an empty
// primary is not "ask again", it is "there is no primary" — which is a reason
// to promote one.
//
// So a caller that acts on a read, rather than merely displays it, should wait
// once after startup. A Barrier is the leader's way of asking; a follower has
// no way to append, so it watches its applied index reach the committed one.
func (n *Node) WaitForApplied(ctx context.Context) error {
	if n == nil || n.raft == nil {
		return nil
	}

	if n.IsLeader() {
		return n.raft.Barrier(applyTimeout).Error()
	}

	// Polled, because hashicorp/raft offers no signal for this on a follower.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if n.raft.AppliedIndex() >= n.raft.LastIndex() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// GetPrimary returns the address the cluster agrees holds the write role.
//
// A local read of applied state. See WaitForApplied before acting on it at
// startup.
func (n *Node) GetPrimary() (string, error) {
	n.fsm.mu.RLock()
	defer n.fsm.mu.RUnlock()
	return n.fsm.state.Primary, nil
}

// SetPrimary records a new holder of the write role.
func (n *Node) SetPrimary(address string) error {
	return n.propose(Command{Op: CmdSetPrimary, Data: []byte(address)})
}

// SyncBackends replicates the backend inventory.
func (n *Node) SyncBackends(backends []*domain.BackendConfig) error {
	data, err := json.Marshal(backends)
	if err != nil {
		return err
	}
	return n.propose(Command{Op: CmdSyncBackends, Data: data})
}

// propose replicates one command and waits for it to be applied.
//
// Both errors are checked. Apply's own error says whether the entry was
// committed; the FSM's response says whether applying it worked. Reporting only
// the first means a command the FSM rejected — malformed data, an unknown op —
// is reported as a success, and the caller believes the cluster agreed to
// something it did not.
func (n *Node) propose(cmd Command) error {
	if !n.IsLeader() {
		return ErrNotLeader
	}

	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	future := n.raft.Apply(b, applyTimeout)
	if err := future.Error(); err != nil {
		return err
	}
	if applyErr, ok := future.Response().(error); ok && applyErr != nil {
		return fmt.Errorf("applying %s: %w", cmd.Op, applyErr)
	}
	return nil
}

// ErrNotLeader reports a write attempted on a node that cannot order it.
var ErrNotLeader = errors.New("not leader")

// applyTimeout bounds how long a proposal waits to be committed.
const applyTimeout = 10 * time.Second

// GetConfig returns the current global configuration from the FSM.
//
// A copy. Returning the FSM's own slice hands a caller a reference to
// replicated state, and anything that writes through it edits the cluster's
// agreed configuration without going through the log — which is exactly the
// "Raft state written outside the FSM" that the whole design forbids.
func (n *Node) GetConfig() []byte {
	n.fsm.mu.RLock()
	defer n.fsm.mu.RUnlock()
	return bytes.Clone(n.fsm.state.Config)
}

// ProposeConfig proposes a new configuration to the cluster.
func (n *Node) ProposeConfig(config []byte) error {
	return n.propose(Command{Op: CmdUpdateConfig, Data: config})
}

// IsLeader returns true if the current node is the leader.
func (n *Node) IsLeader() bool {
	return n.raft.State() == raft.Leader
}

// LeaderAddr returns the address of the current leader.
func (n *Node) LeaderAddr() string {
	_, id := n.raft.LeaderWithID()
	return string(id)
}

// Join adds a new node to the cluster.
func (n *Node) Join(nodeID, addr string) error {
	if !n.IsLeader() {
		return ErrNotLeader
	}

	f := n.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	if err := f.Error(); err != nil {
		return err
	}
	return nil
}

type fsm struct {
	mu    sync.RWMutex
	state clusterState
}

func (f *fsm) Apply(l *raft.Log) any {
	var cmd Command
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch cmd.Op {
	case CmdSyncBackends:
		var backends []*domain.BackendConfig
		if err := json.Unmarshal(cmd.Data, &backends); err != nil {
			return err
		}
		f.state.Backends = backends
	case CmdUpdateConfig:
		f.state.Config = cmd.Data
	case CmdSetPrimary:
		f.state.Primary = string(cmd.Data)
	default:
		// Reported rather than ignored. An entry this node cannot interpret is
		// one a peer on a different version wrote, and silently skipping it
		// means the two disagree about the state while both believe they are
		// in sync.
		return fmt.Errorf("unknown consensus command %q", cmd.Op)
	}
	return nil
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return &snapshot{state: f.state}, nil
}

// Restore replaces this node's state with a snapshot's.
//
// Decoded into a fresh value first. Decoding straight into f.state *merges*:
// a field the snapshot omits — and clusterState omits its zero fields — keeps
// whatever this node happened to have, so restoring a snapshot taken before a
// backend existed leaves that backend in place. A restore that does not replace
// is not a restore.
func (f *fsm) Restore(r io.ReadCloser) error {
	defer r.Close()

	var restored clusterState
	if err := json.NewDecoder(r).Decode(&restored); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = restored
	return nil
}

type snapshot struct {
	state clusterState
}

func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	b, err := json.Marshal(s.state)
	if err != nil {
		sink.Cancel()
		return err
	}
	if _, err := sink.Write(b); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *snapshot) Release() {}

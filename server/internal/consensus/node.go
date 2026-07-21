package consensus

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/hashicorp/raft"
)

type CommandType string

const (
	CmdSyncBackends CommandType = "sync_backends"
	CmdUpdateConfig CommandType = "update_config"
)

type Command struct {
	Op   CommandType `json:"op"`
	Data []byte      `json:"data"`
}

type clusterState struct {
	Backends []*domain.BackendConfig `json:"backends,omitzero"`
	Config   []byte                  `json:"config,omitzero"` // Encoded proxy.Config
}

// Node represents a node in the consensus cluster.
type Node struct {
	raft *raft.Raft
	fsm  *fsm
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

	// For production, we would use a real log store like BoltDB or Badger.
	// For this task, we'll use in-memory stores to keep it lightweight.
	logStore := raft.NewInmemStore()
	stableStore := raft.NewInmemStore()

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
		r.BootstrapCluster(configuration)
	}

	return &Node{raft: r, fsm: fsmInstance}, nil
}

// GetConfig returns the current global configuration from the FSM.
func (n *Node) GetConfig() []byte {
	n.fsm.mu.RLock()
	defer n.fsm.mu.RUnlock()
	return n.fsm.state.Config
}

// ProposeConfig proposes a new configuration to the cluster.
func (n *Node) ProposeConfig(config []byte) error {
	if n.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}

	cmd := Command{
		Op:   CmdUpdateConfig,
		Data: config,
	}
	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	f := n.raft.Apply(b, 10*time.Second)
	return f.Error()
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
	if n.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
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
	}
	return nil
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return &snapshot{state: f.state}, nil
}

func (f *fsm) Restore(r io.ReadCloser) error {
	defer r.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	return json.NewDecoder(r).Decode(&f.state)
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

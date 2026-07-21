package orchestration

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/raft"
)

type raftConsensus struct {
	raft        *raft.Raft
	fsm         *failoverFSM
	address     string
	nodeID      string
	logStore    raft.LogStore
	stableStore raft.StableStore
	snapshots   raft.SnapshotStore
}

func NewRaftConsensus(nodeID, addr string, dataDir string) (Consensus, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)

	// Setup Raft communication
	advertiseAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	transport, err := raft.NewTCPTransport(addr, advertiseAddr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, err
	}

	// Snapshot store
	snapshots, err := raft.NewFileSnapshotStore(dataDir, 2, os.Stderr)
	if err != nil {
		return nil, err
	}

	// In-memory log and stable store
	logStore := raft.NewInmemStore()
	stableStore := raft.NewInmemStore()

	fsm := &failoverFSM{}
	r, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return nil, err
	}

	return &raftConsensus{
		raft:        r,
		fsm:         fsm,
		address:     addr,
		nodeID:      nodeID,
		logStore:    logStore,
		stableStore: stableStore,
		snapshots:   snapshots,
	}, nil
}

func (c *raftConsensus) Start(ctx context.Context) error {
	// If we are the first node, bootstrap the cluster
	hasState, _ := raft.HasExistingState(c.logStore, c.stableStore, c.snapshots)
	if !hasState {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      raft.ServerID(c.nodeID),
					Address: raft.ServerAddress(c.address),
				},
			},
		}
		c.raft.BootstrapCluster(configuration)
	}
	return nil
}

func (c *raftConsensus) IsLeader() bool {
	return c.raft.State() == raft.Leader
}

func (c *raftConsensus) LeaderID() string {
	_, id := c.raft.LeaderWithID()
	return string(id)
}

func (c *raftConsensus) GetPrimary() (string, error) {
	return c.fsm.getPrimary(), nil
}

func (c *raftConsensus) SetPrimary(address string) error {
	if !c.IsLeader() {
		return fmt.Errorf("not leader")
	}
	f := c.raft.Apply([]byte(address), 10*time.Second)
	return f.Error()
}

func (c *raftConsensus) Join(nodeID, addr string) error {
	if !c.IsLeader() {
		return fmt.Errorf("not leader")
	}
	f := c.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	return f.Error()
}

func (c *raftConsensus) Stop() error {
	return c.raft.Shutdown().Error()
}

type failoverFSM struct {
	mu      sync.RWMutex
	primary string
}

func (f *failoverFSM) Apply(l *raft.Log) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.primary = string(l.Data)
	return nil
}

func (f *failoverFSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return &failoverSnapshot{primary: f.primary}, nil
}

func (f *failoverFSM) Restore(rc io.ReadCloser) error {
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.primary = string(data)
	f.mu.Unlock()
	return nil
}

func (f *failoverFSM) getPrimary() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.primary
}

type failoverSnapshot struct {
	primary string
}

func (s *failoverSnapshot) Persist(sink raft.SnapshotSink) error {
	_, err := sink.Write([]byte(s.primary))
	if err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *failoverSnapshot) Release() {}

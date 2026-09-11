package consensus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/hashicorp/raft"
)

// freeAddr reserves a loopback port and gives it back, so a node can bind it.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the port: %v", err)
	}
	return addr
}

// leaderNode starts a single-node cluster and waits for it to elect itself.
func leaderNode(t *testing.T) *Node {
	t.Helper()

	node, err := NewNode("node-1", freeAddr(t), t.TempDir(), true)
	if err != nil {
		t.Fatalf("starting the node: %v", err)
	}
	t.Cleanup(func() {
		if err := node.Stop(); err != nil {
			t.Logf("stopping the node: %v", err)
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if node.IsLeader() {
			return node
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the node never became leader")
	return nil
}

// The failover manager takes a Consensus, and this package exists to be one.
// They were written against different shapes and never met: the node had
// GetConfig and LeaderAddr where the consumer wanted GetPrimary, LeaderID,
// Start and Stop, so nothing could pass one to the other.
func TestNodeSatisfiesTheConsensusContract(t *testing.T) {
	// Declared here rather than importing orchestration, which would be an
	// import cycle. Kept in step by name: every method the interface lists.
	type consensus interface {
		Start(ctx context.Context) error
		IsLeader() bool
		LeaderID() string
		GetPrimary() (string, error)
		SetPrimary(address string) error
		Join(nodeID, addr string) error
		Stop() error
	}

	var node any = (*Node)(nil)
	if _, ok := node.(consensus); !ok {
		t.Error("*Node does not satisfy the Consensus contract the failover manager needs")
	}
}

// A primary agreed through the log is the whole reason to run Raft here.
func TestPrimaryRoundTripsThroughTheLog(t *testing.T) {
	node := leaderNode(t)

	if primary, err := node.GetPrimary(); err != nil || primary != "" {
		t.Errorf("a fresh cluster reports primary %q (err %v), want empty", primary, err)
	}

	if err := node.SetPrimary("10.0.0.5:5432"); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}
	primary, err := node.GetPrimary()
	if err != nil {
		t.Fatalf("GetPrimary: %v", err)
	}
	if primary != "10.0.0.5:5432" {
		t.Errorf("primary = %q, want 10.0.0.5:5432", primary)
	}

	// A later promotion replaces it rather than accumulating.
	if err := node.SetPrimary("10.0.0.6:5432"); err != nil {
		t.Fatalf("SetPrimary again: %v", err)
	}
	if primary, _ = node.GetPrimary(); primary != "10.0.0.6:5432" {
		t.Errorf("primary = %q, want the newer 10.0.0.6:5432", primary)
	}
}

func TestConfigRoundTripsThroughTheLog(t *testing.T) {
	node := leaderNode(t)

	want := []byte("proxy_addr: :5432\n")
	if err := node.ProposeConfig(want); err != nil {
		t.Fatalf("ProposeConfig: %v", err)
	}
	if got := node.GetConfig(); !bytes.Equal(got, want) {
		t.Errorf("GetConfig = %q, want %q", got, want)
	}
}

// Handing out the FSM's own slice lets a caller edit replicated state without
// going through the log, which is the one thing the design forbids.
func TestGetConfigReturnsACopy(t *testing.T) {
	node := leaderNode(t)

	if err := node.ProposeConfig([]byte("original")); err != nil {
		t.Fatalf("ProposeConfig: %v", err)
	}

	got := node.GetConfig()
	got[0] = 'X'

	if after := node.GetConfig(); string(after) != "original" {
		t.Errorf("the FSM's state became %q after a caller wrote to what it returned", after)
	}
}

// A write on a node that cannot order it has to be refused, not queued.
func TestWritesRequireLeadership(t *testing.T) {
	follower, err := NewNode("lonely", freeAddr(t), t.TempDir(), false)
	if err != nil {
		t.Fatalf("starting the node: %v", err)
	}
	t.Cleanup(func() { _ = follower.Stop() })

	// Never bootstrapped, so it has no cluster and cannot be leader.
	if follower.IsLeader() {
		t.Fatal("a node that was never bootstrapped reports itself leader")
	}

	for name, err := range map[string]error{
		"SetPrimary":    follower.SetPrimary("10.0.0.5:5432"),
		"ProposeConfig": follower.ProposeConfig([]byte("x")),
		"Join":          follower.Join("other", "127.0.0.1:1"),
	} {
		if !errors.Is(err, ErrNotLeader) {
			t.Errorf("%s on a follower returned %v, want ErrNotLeader", name, err)
		}
	}
}

// An entry the FSM rejects must not be reported as agreement.
//
// Apply's own error says the entry was committed; the FSM's response says
// whether applying it worked. Reporting only the first tells a caller the
// cluster agreed to something it refused.
func TestApplyErrorsAreNotReportedAsSuccess(t *testing.T) {
	node := leaderNode(t)

	err := node.propose(Command{Op: "no-such-op", Data: []byte("{}")})
	if err == nil {
		t.Fatal("a command the FSM rejected was reported as applied")
	}
	if !strings.Contains(err.Error(), "unknown consensus command") {
		t.Errorf("error does not name the cause: %v", err)
	}

	// Malformed data for a known command is the same story.
	err = node.propose(Command{Op: CmdSyncBackends, Data: []byte("not json")})
	if err == nil {
		t.Error("a command with unparseable data was reported as applied")
	}
}

func TestBackendsRoundTripThroughTheLog(t *testing.T) {
	node := leaderNode(t)

	want := []*domain.BackendConfig{
		{Address: "10.0.0.5:5432", Role: "primary"},
		{Address: "10.0.0.6:5432", Role: "replica"},
	}
	if err := node.SyncBackends(want); err != nil {
		t.Fatalf("SyncBackends: %v", err)
	}

	node.fsm.mu.RLock()
	got := node.fsm.state.Backends
	node.fsm.mu.RUnlock()

	if len(got) != len(want) {
		t.Fatalf("got %d backends, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Address != want[i].Address || got[i].Role != want[i].Role {
			t.Errorf("backend %d = %q/%q, want %q/%q",
				i, got[i].Address, got[i].Role, want[i].Address, want[i].Role)
		}
	}
}

// A restore replaces state; it does not merge into it.
//
// clusterState omits its zero fields, so decoding a snapshot straight into live
// state leaves whatever this node happened to have for anything the snapshot
// does not mention. Restoring a snapshot taken before a backend existed would
// leave that backend in place.
func TestRestoreReplacesRatherThanMerges(t *testing.T) {
	f := &fsm{state: clusterState{
		Primary:  "10.0.0.9:5432",
		Config:   []byte("stale"),
		Backends: []*domain.BackendConfig{{Address: "gone:5432"}},
	}}

	// A snapshot of an empty cluster: every field is zero, so every field is
	// omitted.
	empty, err := json.Marshal(clusterState{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Restore(io.NopCloser(bytes.NewReader(empty))); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if f.state.Primary != "" {
		t.Errorf("primary survived the restore as %q", f.state.Primary)
	}
	if f.state.Config != nil {
		t.Errorf("config survived the restore as %q", f.state.Config)
	}
	if f.state.Backends != nil {
		t.Errorf("%d backends survived the restore", len(f.state.Backends))
	}
}

// What a snapshot persists is what a restore must reproduce.
func TestSnapshotRoundTrips(t *testing.T) {
	f := &fsm{state: clusterState{
		Primary:  "10.0.0.5:5432",
		Config:   []byte("proxy_addr: :5432"),
		Backends: []*domain.BackendConfig{{Address: "10.0.0.5:5432", Role: "primary"}},
	}}

	snap, err := f.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	sink := &memorySink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	snap.Release()

	restored := &fsm{}
	if err := restored.Restore(io.NopCloser(bytes.NewReader(sink.Bytes()))); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if restored.state.Primary != f.state.Primary {
		t.Errorf("primary = %q, want %q", restored.state.Primary, f.state.Primary)
	}
	if !bytes.Equal(restored.state.Config, f.state.Config) {
		t.Errorf("config = %q, want %q", restored.state.Config, f.state.Config)
	}
	if len(restored.state.Backends) != 1 || restored.state.Backends[0].Address != "10.0.0.5:5432" {
		t.Errorf("backends = %+v, want one at 10.0.0.5:5432", restored.state.Backends)
	}
}

// Stop has to release the listener. Without it a node leaves a port bound and
// its goroutines running for the life of the process.
func TestStopReleasesTheListener(t *testing.T) {
	addr := freeAddr(t)

	node, err := NewNode("node-1", addr, t.TempDir(), true)
	if err != nil {
		t.Fatalf("starting the node: %v", err)
	}
	if err := node.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// The port is free again, which is only true if the transport was closed.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the node's port is still bound after Stop: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	// And stopping twice is not an error a shutdown path has to guard against.
	if err := node.Stop(); err != nil {
		t.Logf("second Stop returned %v", err)
	}
}

// A bootstrap that fails must not return a node that can never elect a leader
// and never says why.
func TestBootstrapFailureIsReported(t *testing.T) {
	// An address nothing can bind.
	_, err := NewNode("node-1", "256.256.256.256:1", t.TempDir(), true)
	if err == nil {
		t.Fatal("a node was returned for an address that cannot be bound")
	}
}

// memorySink is a raft.SnapshotSink that keeps what it is given.
type memorySink struct {
	bytes.Buffer
	cancelled bool
}

func (s *memorySink) ID() string { return "test" }
func (s *memorySink) Close() error {
	return nil
}
func (s *memorySink) Cancel() error {
	s.cancelled = true
	return nil
}

var _ raft.SnapshotSink = (*memorySink)(nil)

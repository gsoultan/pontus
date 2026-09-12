package consensus

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Multi-node tests, because a one-node cluster tests none of the consensus.
//
// A single node elects itself unopposed and commits every entry locally, so the
// single-node tests in node_test.go exercise the FSM and the API and nothing
// else. Election between peers, replication to a follower, what happens when the
// leader dies, and the refusal to commit without a quorum — those are the
// properties the package exists for, and none of them appears with one node.
//
// In-process rather than in containers: a Raft node is a process with a TCP
// address, and three of them on loopback exercise the same transport, election
// and replication code that three hosts would. Containers would add isolation
// this does not need and minutes this should not take.

// cluster is a set of nodes that know about each other.
type cluster struct {
	t     *testing.T
	nodes map[string]*Node
}

// startCluster brings up n nodes and joins them into one cluster.
func startCluster(t *testing.T, n int) *cluster {
	t.Helper()

	c := &cluster{t: t, nodes: make(map[string]*Node, n)}

	// The first bootstraps; the rest wait to be added, because two nodes that
	// each bootstrap form two clusters of one and neither ever learns of the
	// other.
	for i := range n {
		id := fmt.Sprintf("node-%d", i+1)
		node, err := NewNode(id, freeAddr(t), t.TempDir(), i == 0)
		if err != nil {
			t.Fatalf("starting %s: %v", id, err)
		}
		c.nodes[id] = node
		t.Cleanup(func() { _ = node.Stop() })
	}

	leader := c.waitLeader(10 * time.Second)
	for id, node := range c.nodes {
		if node == leader {
			continue
		}
		if err := leader.Join(id, string(node.transport.LocalAddr())); err != nil {
			t.Fatalf("joining %s: %v", id, err)
		}
	}

	// Joining changes the quorum, so wait for the cluster to settle at the new
	// size before a test starts asserting about it.
	c.waitLeader(10 * time.Second)
	return c
}

// waitLeader blocks until exactly one node claims leadership.
func (c *cluster) waitLeader(within time.Duration) *Node {
	c.t.Helper()

	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if leaders := c.leaders(); len(leaders) == 1 {
			return leaders[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("no single leader within %v; leaders: %d", within, len(c.leaders()))
	return nil
}

func (c *cluster) leaders() []*Node {
	var out []*Node
	for _, node := range c.nodes {
		if node.IsLeader() {
			out = append(out, node)
		}
	}
	return out
}

func (c *cluster) followers() []*Node {
	var out []*Node
	for _, node := range c.nodes {
		if !node.IsLeader() {
			out = append(out, node)
		}
	}
	return out
}

// Exactly one leader. Two would mean two control planes each believing they may
// promote, which is the failure the whole package exists to prevent.
func TestClusterElectsOneLeader(t *testing.T) {
	c := startCluster(t, 3)

	leader := c.waitLeader(10 * time.Second)
	if leader == nil {
		t.Fatal("no leader")
	}

	// Every node agrees who it is.
	for id, node := range c.nodes {
		if got := node.LeaderID(); got != leader.id {
			t.Errorf("%s thinks the leader is %q, want %q", id, got, leader.id)
		}
	}
}

// A write on the leader has to reach the followers. Without this the cluster
// agrees on a leader and nothing else, and a failover would hand the write role
// to a node that never learned who held it.
func TestWritesReplicateToFollowers(t *testing.T) {
	c := startCluster(t, 3)
	leader := c.waitLeader(10 * time.Second)

	if err := leader.SetPrimary("10.0.0.5:5432"); err != nil {
		t.Fatalf("SetPrimary on the leader: %v", err)
	}

	for _, follower := range c.followers() {
		if !waitFor(5*time.Second, func() bool {
			primary, err := follower.GetPrimary()
			return err == nil && primary == "10.0.0.5:5432"
		}) {
			primary, _ := follower.GetPrimary()
			t.Errorf("%s has primary %q, want 10.0.0.5:5432", follower.id, primary)
		}
	}
}

// A follower must refuse a write rather than apply it locally. A follower that
// accepted one would diverge from the cluster silently.
func TestFollowersRefuseWrites(t *testing.T) {
	c := startCluster(t, 3)
	c.waitLeader(10 * time.Second)

	for _, follower := range c.followers() {
		if err := follower.SetPrimary("10.0.0.9:5432"); err == nil {
			t.Errorf("%s accepted a write while not leader", follower.id)
		}
	}

	// And nothing was applied anywhere.
	for id, node := range c.nodes {
		if primary, _ := node.GetPrimary(); primary == "10.0.0.9:5432" {
			t.Errorf("%s applied a follower's write", id)
		}
	}
}

// The leader dies and the survivors carry on. This is the scenario the failover
// manager is asking consensus about in the first place: the control plane that
// held the write role is gone, and the remaining ones must agree on who decides
// next rather than each deciding for itself.
func TestClusterSurvivesLosingItsLeader(t *testing.T) {
	c := startCluster(t, 3)
	leader := c.waitLeader(10 * time.Second)

	if err := leader.SetPrimary("10.0.0.5:5432"); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}
	for _, f := range c.followers() {
		waitFor(5*time.Second, func() bool {
			p, _ := f.GetPrimary()
			return p == "10.0.0.5:5432"
		})
	}

	// The leader goes away.
	dead := leader.id
	if err := leader.Stop(); err != nil {
		t.Fatalf("stopping the leader: %v", err)
	}
	delete(c.nodes, dead)

	// Two of three remain, which is still a quorum.
	fresh := c.waitLeader(20 * time.Second)
	if fresh.id == dead {
		t.Fatal("the dead node is still reported as leader")
	}

	// The committed state survived the handover.
	if primary, _ := fresh.GetPrimary(); primary != "10.0.0.5:5432" {
		t.Errorf("the new leader has primary %q, want the committed 10.0.0.5:5432", primary)
	}

	// And it can still commit.
	if err := fresh.SetPrimary("10.0.0.6:5432"); err != nil {
		t.Errorf("the new leader cannot commit: %v", err)
	}
}

// Without a quorum, nothing commits.
//
// This is the property that makes consensus worth running. A minority that kept
// accepting writes would be exactly the split brain the failover manager
// consults consensus to avoid — two partitions each promoting their own primary.
func TestMinorityCannotCommit(t *testing.T) {
	c := startCluster(t, 3)
	leader := c.waitLeader(10 * time.Second)

	// Take away two of three, leaving the old leader in a minority of one.
	for _, follower := range c.followers() {
		if err := follower.Stop(); err != nil {
			t.Fatalf("stopping %s: %v", follower.id, err)
		}
		delete(c.nodes, follower.id)
	}

	// It either steps down, or stays leader and cannot commit. Both are
	// correct; what must not happen is a committed write.
	err := leader.SetPrimary("10.0.0.9:5432")
	if err == nil {
		t.Fatal("a minority of one committed a write")
	}
	t.Logf("minority write refused: %v", err)
}

// waitFor polls until cond holds or the budget runs out.
func waitFor(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// A node that restarts must remember what the cluster committed.
//
// Raft's safety rests on durable state. The log holds the entries a node has
// acknowledged, and the stable store holds `currentTerm` and `votedFor` — the
// two values that stop a node voting twice in one term. A node that forgets
// them and comes back can elect a second leader in a term that already has one,
// which is precisely the split brain the failover manager runs consensus to
// avoid. "Lightweight" in-memory stores do not make this slower, they make it
// unsound.
func TestCommittedStateSurvivesARestart(t *testing.T) {
	addr := freeAddr(t)
	dataDir := t.TempDir()

	node, err := NewNode("node-1", addr, dataDir, true)
	if err != nil {
		t.Fatalf("starting the node: %v", err)
	}
	if !waitFor(10*time.Second, node.IsLeader) {
		t.Fatal("the node never became leader")
	}

	if err := node.SetPrimary("10.0.0.5:5432"); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}
	if err := node.ProposeConfig([]byte("proxy_addr: :5432")); err != nil {
		t.Fatalf("ProposeConfig: %v", err)
	}
	if err := node.Stop(); err != nil {
		t.Fatalf("stopping the node: %v", err)
	}

	// The same identity and the same directory: this is a restart, not a new
	// cluster. Bootstrap is requested exactly as a service unit would on every
	// start; a node with persisted state must ignore it rather than start over.
	restarted, err := NewNode("node-1", addr, dataDir, true)
	if err != nil {
		t.Fatalf("restarting the node: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Stop() })

	if !waitFor(10*time.Second, restarted.IsLeader) {
		t.Fatal("the restarted node never became leader")
	}

	// Leadership arrives before the replayed log has been applied, so a read
	// taken straight after it can be empty rather than stale. This is the wait
	// a caller that acts on the answer has to do.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := restarted.WaitForApplied(ctx); err != nil {
		t.Fatalf("waiting for the replayed log to be applied: %v", err)
	}

	if primary, _ := restarted.GetPrimary(); primary != "10.0.0.5:5432" {
		t.Errorf("primary after a restart = %q, want the committed 10.0.0.5:5432", primary)
	}
	if config := restarted.GetConfig(); string(config) != "proxy_addr: :5432" {
		t.Errorf("config after a restart = %q, want the committed value", config)
	}
}

// A read taken before the log has been applied is empty, not stale, and to the
// failover manager an empty primary is a reason to promote one. WaitForApplied
// is what a caller that acts on the answer uses.
func TestWaitForAppliedCatchesUpAFollower(t *testing.T) {
	c := startCluster(t, 3)
	leader := c.waitLeader(10 * time.Second)

	if err := leader.SetPrimary("10.0.0.5:5432"); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, follower := range c.followers() {
		if err := follower.WaitForApplied(ctx); err != nil {
			t.Fatalf("%s: %v", follower.id, err)
		}
		// After the wait, no polling: the answer is there.
		if primary, _ := follower.GetPrimary(); primary != "10.0.0.5:5432" {
			t.Errorf("%s has primary %q after WaitForApplied, want 10.0.0.5:5432",
				follower.id, primary)
		}
	}
}

// A cancelled wait returns rather than blocking, so a shutdown is not held up
// by a follower that will never catch up.
func TestWaitForAppliedHonoursCancellation(t *testing.T) {
	node, err := NewNode("lonely", freeAddr(t), t.TempDir(), false)
	if err != nil {
		t.Fatalf("starting the node: %v", err)
	}
	t.Cleanup(func() { _ = node.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Never bootstrapped, so it is a follower of nothing. The only question is
	// whether it returns.
	done := make(chan error, 1)
	go func() { done <- node.WaitForApplied(ctx) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("WaitForApplied ignored a cancelled context")
	}
}

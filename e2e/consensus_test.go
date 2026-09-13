//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Raft between real Pontus processes.
//
// The consensus package has its own multi-node tests, which run three Raft nodes
// in one process. This is the other question: does a deployment configured for
// consensus actually elect one leader, and do the others stand down from acting
// on the cluster? That is config, wiring and startup order, none of which the
// unit tests touch.
//
// Three processes rather than three containers: each is a Pontus with its own
// data directory and ports, which is what three hosts would be. The difference a
// container would add is network isolation, and nothing here depends on it.

// consensusStack is one Pontus configured as a Raft node.
type consensusStack struct {
	*stack
	nodeID   string
	raftAddr string
}

// startConsensusCluster brings up n Pontus processes that agree with each other.
func startConsensusCluster(t *testing.T, n int) []*consensusStack {
	t.Helper()
	requireBackend(t)

	root := repoRoot(t)
	binary := filepath.Join(t.TempDir(), "pontus")
	build := exec.Command("go", "build", "-o", binary, "./cmd/pontus")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pontus: %v\n%s", err, out)
	}

	// Every port up front, so each node's config can name the others.
	ports, release := freePorts(t, n*3)
	nodes := make([]*consensusStack, n)
	for i := range n {
		nodes[i] = &consensusStack{
			nodeID:   fmt.Sprintf("pontus-%d", i+1),
			raftAddr: fmt.Sprintf("127.0.0.1:%d", ports[i*3+2]),
		}
	}
	release()

	for i := range n {
		dataDir := t.TempDir()
		proxyAddr := fmt.Sprintf("127.0.0.1:%d", ports[i*3])
		mgmtAddr := fmt.Sprintf("127.0.0.1:%d", ports[i*3+1])

		// The first bootstraps and adds the others. Several bootstrapping nodes
		// form several clusters of one, each with its own leader and none aware
		// of the others.
		config := configYAML(dataDir, proxyAddr, mgmtAddr) +
			consensusBlock(nodes[i].nodeID, nodes[i].raftAddr, i == 0, nodes)

		configPath := filepath.Join(dataDir, "config.yaml")
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}

		logs := &logSink{}
		cmd := exec.Command(binary, "-config", configPath)
		cmd.Dir = root
		cmd.Stdout = logs
		cmd.Stderr = logs
		cmd.Env = append(os.Environ(), "PONTUS_AUTH_KEY="+authKey)
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting %s: %v", nodes[i].nodeID, err)
		}

		nodes[i].stack = &stack{
			cmd: cmd, dataDir: dataDir, logs: logs, t: t,
			proxyAddr: proxyAddr, mgmtAddr: mgmtAddr,
		}
		t.Cleanup(nodes[i].stack.stop)
		nodes[i].stack.waitListening(mgmtAddr)
	}

	return nodes
}

// consensusBlock is the config that makes one node part of the cluster.
func consensusBlock(nodeID, bindAddr string, bootstrap bool, all []*consensusStack) string {
	var peers strings.Builder
	if bootstrap {
		for _, peer := range all {
			if peer.nodeID == nodeID {
				continue
			}
			fmt.Fprintf(&peers, "    - node_id: %s\n      addr: \"%s\"\n", peer.nodeID, peer.raftAddr)
		}
	}

	block := fmt.Sprintf(`
consensus:
  enabled: true
  node_id: %s
  bind_addr: "%s"
  bootstrap: %t
`, nodeID, bindAddr, bootstrap)

	if peers.Len() > 0 {
		block += "  peers:\n" + peers.String()
	}
	return block
}

// Exactly one leader across the deployment. Two would mean two control planes
// each believing they may promote, which is what running Raft is for.
func TestConsensusElectsOneLeaderAcrossProcesses(t *testing.T) {
	nodes := startConsensusCluster(t, 3)

	// Each node says whether it holds leadership; exactly one should.
	if !waitFor(60*time.Second, func() bool { return countLeaders(nodes) == 1 }) {
		t.Fatalf("no single leader: %d claim it\n%s", countLeaders(nodes), clusterLogs(nodes))
	}

	// And it stays that way rather than flapping.
	for range 5 {
		time.Sleep(time.Second)
		if n := countLeaders(nodes); n != 1 {
			t.Fatalf("leadership is unstable: %d leaders\n%s", n, clusterLogs(nodes))
		}
	}
}

// A node that is not the leader must stand down from acting on the cluster.
// Without that, running Raft changes nothing: every node still promotes.
func TestFollowersStandDownFromOrchestration(t *testing.T) {
	nodes := startConsensusCluster(t, 3)

	if !waitFor(60*time.Second, func() bool { return countLeaders(nodes) == 1 }) {
		t.Fatalf("no single leader\n%s", clusterLogs(nodes))
	}

	// Every node reports that consensus started, so none of them silently fell
	// back to acting alone.
	for _, node := range nodes {
		if !strings.Contains(node.logs.String(), "Consensus started") {
			t.Errorf("%s never started consensus:\n%s",
				node.nodeID, tailLog(node.logs.String(), 2000))
		}
		if strings.Contains(node.logs.String(), "Consensus is enabled but could not start") {
			t.Errorf("%s failed to start consensus:\n%s",
				node.nodeID, tailLog(node.logs.String(), 2000))
		}
	}
}

// countLeaders asks each node's log whether it took leadership.
//
// Read from the log rather than an RPC because leadership is not on the
// management API: what matters here is that the cluster converged, and Raft
// announces both directions.
func countLeaders(nodes []*consensusStack) int {
	var leaders int
	for _, node := range nodes {
		if isLeaderNow(node.logs.String()) {
			leaders++
		}
	}
	return leaders
}

// isLeaderNow reports whether the last leadership transition in a log was into
// leadership rather than out of it.
func isLeaderNow(log string) bool {
	entered := strings.LastIndex(log, "entering leader state")
	left := strings.LastIndex(log, "entering follower state")
	if entered < 0 {
		return false
	}
	return entered > left
}

func clusterLogs(nodes []*consensusStack) string {
	var out strings.Builder
	for _, node := range nodes {
		fmt.Fprintf(&out, "\n=== %s ===\n%s\n", node.nodeID, tailLog(node.logs.String(), 1500))
	}
	return out.String()
}

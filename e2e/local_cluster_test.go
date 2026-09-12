//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A two-node cluster where the agent outlives the database.
//
// `scripts/e2e-cluster.sh` runs PostgreSQL as PID 1 with the agent beside it,
// so stopping the database tears the container down and takes the agent with
// it. A rebuild has to stop the database, so that topology can never complete
// one — the agent refuses up front rather than starting what it cannot finish.
//
// Here both clusters and both agents are ordinary processes on this machine,
// which is the shape of the VM or systemd deployment the agent is built for:
// `pg_ctl stop` ends the database and the agent carries on. It is the only
// topology in which automatic fallback can be measured end to end.
//
// No container runtime and no image build — the PostgreSQL binaries are enough.

const localPassword = "pontus-local-e2e"

// localNode is one PostgreSQL instance and the agent managing it.
type localNode struct {
	dataDir   string
	port      int
	agentPort int
	logPath   string
	agent     *exec.Cmd
	agentLog  string
}

func (n *localNode) addr() string      { return fmt.Sprintf("127.0.0.1:%d", n.port) }
func (n *localNode) agentAddr() string { return fmt.Sprintf("127.0.0.1:%d", n.agentPort) }

func (n *localNode) dsn() string {
	return fmt.Sprintf("postgres://postgres:%s@%s/postgres?sslmode=disable", localPassword, n.addr())
}

// localCluster is a primary, a standby streaming from it, and their agents.
type localCluster struct {
	primary *localNode
	standby *localNode
	t       *testing.T
}

func requireLocalPostgres(t *testing.T) {
	t.Helper()
	for _, binary := range []string{"initdb", "pg_ctl", "pg_basebackup", "postgres"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not on PATH; install the PostgreSQL server package", binary)
		}
	}
}

// startLocalCluster builds the pair and waits until the standby is streaming.
func startLocalCluster(t *testing.T) *localCluster {
	t.Helper()
	requireLocalPostgres(t)

	// Not t.TempDir(): PostgreSQL refuses a data directory whose path is too
	// long for a unix socket, and macOS temp paths are long enough to matter.
	root, err := os.MkdirTemp("/tmp", "pontus-lc-")
	if err != nil {
		t.Fatalf("creating the cluster root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	ports, release := freePorts(t, 4)
	c := &localCluster{
		t: t,
		primary: &localNode{
			dataDir: filepath.Join(root, "primary"), port: ports[0], agentPort: ports[2],
			logPath: filepath.Join(root, "primary.log"), agentLog: filepath.Join(root, "primary-agent.log"),
		},
		standby: &localNode{
			dataDir: filepath.Join(root, "standby"), port: ports[1], agentPort: ports[3],
			logPath: filepath.Join(root, "standby.log"), agentLog: filepath.Join(root, "standby-agent.log"),
		},
	}
	release()

	pwFile := filepath.Join(root, "pw")
	if err := os.WriteFile(pwFile, []byte(localPassword), 0o600); err != nil {
		t.Fatalf("writing the password file: %v", err)
	}

	t.Cleanup(c.stop)

	// --locale=C keeps initdb off this machine's locale, which is the usual
	// reason this step behaves differently elsewhere.
	// Local socket connections are trusted, host connections are not. That is
	// the shape of a database host: the co-located agent authenticates over the
	// socket as the cluster's superuser without a password, while everything
	// arriving over TCP — the proxy, the tests — proves who it is.
	pgRun(t, "initdb", "-D", c.primary.dataDir, "-U", "postgres",
		"--auth-local=trust", "--auth-host=scram-sha-256",
		"--pwfile="+pwFile, "--locale=C", "--encoding=UTF8")

	appendTo(t, filepath.Join(c.primary.dataDir, "postgresql.conf"), fmt.Sprintf(`
port = %d
listen_addresses = '127.0.0.1'
wal_level = replica
max_wal_senders = 10
hot_standby = on
`, c.primary.port))

	c.start(c.primary)
	c.waitServing(c.primary, 60*time.Second)

	// -R writes standby.signal and primary_conninfo, so the copy comes back a
	// standby rather than a second primary.
	pgRun(t, "pg_basebackup", "-h", "127.0.0.1", "-p", strconv.Itoa(c.primary.port),
		"-U", "postgres", "-D", c.standby.dataDir, "-R", "-X", "stream", "-c", "fast")
	appendTo(t, filepath.Join(c.standby.dataDir, "postgresql.conf"),
		fmt.Sprintf("\nport = %d\n", c.standby.port))

	c.start(c.standby)
	c.waitServing(c.standby, 60*time.Second)
	c.waitStreaming(60 * time.Second)

	c.startAgent(c.primary)
	c.startAgent(c.standby)
	return c
}

func (c *localCluster) start(n *localNode) {
	c.t.Helper()
	pgRun(c.t, "pg_ctl", "-D", n.dataDir, "-l", n.logPath, "-w", "-t", "60", "start")
}

// stopNode crashes a node. Immediate rather than fast: a clean shutdown lets
// PostgreSQL tell its standby it is going away, which would make this a
// handover rather than the failure a failover test needs.
func (c *localCluster) stopNode(n *localNode) {
	c.t.Helper()
	cmd := exec.Command("pg_ctl", "-D", n.dataDir, "-m", "immediate", "-w", "-t", "60", "stop")
	if out, err := cmd.CombinedOutput(); err != nil {
		c.t.Logf("stopping %s: %v\n%s", n.addr(), err, out)
	}
}

// startAgent runs a pontus-agent for one node, as its own process.
//
// This is the whole point of the topology: the agent is not a child of the
// database and is not torn down with it, so it can stop PostgreSQL, rebuild the
// cluster and start it again.
func (c *localCluster) startAgent(n *localNode) {
	c.t.Helper()

	binary := buildLocalAgent(c.t)
	log, err := os.Create(n.agentLog)
	if err != nil {
		c.t.Fatalf("creating the agent log: %v", err)
	}

	// -data-dir rather than letting the agent scan: this machine's scan finds
	// whatever cluster a package manager installed, not the one under test.
	cmd := exec.Command(binary, "-addr", n.agentAddr(), "-token", localAgentToken,
		"-data-dir", n.dataDir)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		c.t.Fatalf("starting the agent for %s: %v", n.addr(), err)
	}
	n.agent = cmd

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if pgReachable(n.agentAddr()) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.t.Fatalf("the agent for %s never listened on %s\n%s",
		n.addr(), n.agentAddr(), readLog(n.agentLog))
}

func (c *localCluster) stop() {
	for _, n := range []*localNode{c.standby, c.primary} {
		if n == nil {
			continue
		}
		if n.agent != nil && n.agent.Process != nil {
			_ = n.agent.Process.Kill()
			_ = n.agent.Wait()
		}
		_ = exec.Command("pg_ctl", "-D", n.dataDir, "-m", "immediate", "stop").Run()
	}
}

func (c *localCluster) waitServing(n *localNode, within time.Duration) {
	c.t.Helper()

	deadline := time.Now().Add(within)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, err := pgx.Connect(ctx, n.dsn())
		if err == nil {
			var one int
			err = conn.QueryRow(ctx, "SELECT 1").Scan(&one)
			conn.Close(context.Background())
		}
		cancel()
		if err == nil {
			return
		}
		last = err
		time.Sleep(200 * time.Millisecond)
	}
	c.t.Fatalf("%s never served a query: %v\n%s", n.addr(), last, readLog(n.logPath))
}

// waitStreaming asks the *primary*, not the standby.
//
// A standby cut off from its primary reports its own last state perfectly
// happily — it replayed everything it received and then stopped receiving — so
// asking it whether it is streaming is the trap `mem:failover` records.
func (c *localCluster) waitStreaming(within time.Duration) {
	c.t.Helper()

	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if state, err := queryString(c.primary.dsn(),
			"SELECT state FROM pg_stat_replication LIMIT 1"); err == nil && state == "streaming" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.t.Fatalf("the standby never began streaming\n%s", readLog(c.primary.logPath))
}

// config is the Pontus configuration for this pair.
//
// No peer_addr: both nodes and the proxy share a loopback, so the address the
// proxy uses is the address the nodes use. That is the flat-network case the
// fallback exists for.
func (c *localCluster) config(dataDir, proxyAddr, mgmtAddr string) string {
	return fmt.Sprintf(`proxy_addr: "%s"
mgmt_addr: "%s"
protocol: postgres
pooling_mode: transaction
balancer: p2c
data_dir: "%s"

dial_timeout: 5s
health_interval: 1s
query_timeout: 30s
max_conns: 10
min_idle: 0

backends:
  - addr: "%s"
    agent_addr: "%s"
    agent_token: "%s"
    role: primary
    weight: 1
    admin_dsn: "%s"
    data_dir: "%s"
  - addr: "%s"
    agent_addr: "%s"
    agent_token: "%s"
    role: replica
    weight: 1
    admin_dsn: "%s"
    data_dir: "%s"

failover:
  enabled: true
  failure_threshold: 2
  follow_primary: false
  auto_reattach: true
  auto_rejoin: true
  auto_rejoin_interval: 5s
  auto_rejoin_timeout: 180s
  auto_rejoin_max_attempts: 5
`,
		proxyAddr, mgmtAddr, dataDir,
		c.primary.addr(), c.primary.agentAddr(), localAgentToken, c.primary.dsn(), c.primary.dataDir,
		c.standby.addr(), c.standby.agentAddr(), localAgentToken, c.standby.dsn(), c.standby.dataDir,
	)
}

// startPontus runs a proxy against this cluster.
func (c *localCluster) startPontus(t *testing.T) *stack {
	t.Helper()

	root := repoRoot(t)
	dataDir := t.TempDir()
	binary := filepath.Join(dataDir, "pontus")

	build := exec.Command("go", "build", "-o", binary, "./cmd/pontus")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pontus: %v\n%s", err, out)
	}

	ports, release := freePorts(t, 2)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", ports[0])
	mgmtAddr := fmt.Sprintf("127.0.0.1:%d", ports[1])
	release()

	configPath := filepath.Join(dataDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(c.config(dataDir, proxyAddr, mgmtAddr)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	logs := &logSink{}
	cmd := exec.Command(binary, "-config", configPath)
	cmd.Dir = root
	cmd.Stdout = logs
	cmd.Stderr = logs
	cmd.Env = append(os.Environ(), "PONTUS_AUTH_KEY="+authKey)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pontus: %v", err)
	}

	s := &stack{
		cmd: cmd, dataDir: dataDir, logs: logs, t: t,
		proxyAddr: proxyAddr, mgmtAddr: mgmtAddr,
	}
	t.Cleanup(s.stop)

	s.waitListening(proxyAddr)
	s.waitListening(mgmtAddr)
	return s
}

const localAgentToken = "local-e2e-agent-token"

// buildAgent compiles the agent for this machine, once per run.
func buildLocalAgent(t *testing.T) string {
	t.Helper()

	// Built every run, into the test's own directory. A cached binary in a
	// shared location is a trap: it silently keeps running the code from
	// whenever it was first built, so a change to the agent appears to have no
	// effect at all.
	binary := filepath.Join(t.TempDir(), "pontus-agent")

	build := exec.Command("go", "build", "-o", binary, "./cmd/agent")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the agent: %v\n%s", err, out)
	}
	return binary
}

func pgRun(t *testing.T, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+localPassword)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("appending to %s: %v", path, err)
	}
}

// queryString runs a single-value query, closing its connection immediately.
func queryString(dsn, sql string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer conn.Close(context.Background())

	var out string
	err = conn.QueryRow(ctx, sql).Scan(&out)
	return out, err
}

func queryBool(dsn, sql string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return false, err
	}
	defer conn.Close(context.Background())

	var out bool
	err = conn.QueryRow(ctx, sql).Scan(&out)
	return out, err
}

func queryInt(dsn, sql string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return 0, err
	}
	defer conn.Close(context.Background())

	var out int
	err = conn.QueryRow(ctx, sql).Scan(&out)
	return out, err
}

func readLog(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no log)"
	}
	return tailLog(string(b), 4000)
}

// proxySession opens a session through the proxy.
func proxySession(t *testing.T, ctx context.Context, s *stack) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://postgres:%s@%s/postgres?sslmode=disable",
		localPassword, s.proxyAddr))
	if err != nil {
		t.Fatalf("connecting through the proxy: %v", err)
	}
	return conn
}

// directConn opens a session straight to a node, bypassing the proxy.
func directConn(t *testing.T, ctx context.Context, dsn string) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to %s: %v", dsn, err)
	}
	return conn
}

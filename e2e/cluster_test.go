//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A primary plus a real streaming replica, stood up by scripts/e2e-cluster.sh.
//
// Everything in the rest of the suite runs against a single primary, which
// means load balancing, replica routing, staleness gating and role changes have
// only ever been exercised against mocks. Those are the parts that decide which
// database a query reaches, so a mock proving the predicate is not the same as
// proving the behaviour — twice in this codebase a routing feature passed its
// unit test and did nothing in a running proxy.
func replicaAddr() string { return os.Getenv("PONTUS_E2E_REPLICA") }

// requireCluster skips unless both nodes are reachable and actually related.
func requireCluster(t *testing.T) {
	t.Helper()
	requireBackend(t)

	if replicaAddr() == "" {
		t.Skip("no replica configured (run ./scripts/e2e-cluster.sh up, " +
			"then set PONTUS_E2E_BACKEND and PONTUS_E2E_REPLICA)")
	}
	conn, err := net.DialTimeout("tcp", replicaAddr(), 2*time.Second)
	if err != nil {
		t.Skipf("no PostgreSQL at %s: %v", replicaAddr(), err)
	}
	conn.Close()

	// A second primary would silently turn every routing assertion below into a
	// coin flip, so confirm the topology rather than assuming it.
	if !inRecovery(t, replicaAddr()) {
		t.Fatalf("%s is not a replica; ./scripts/e2e-cluster.sh up builds one", replicaAddr())
	}
	if inRecovery(t, backendAddr()) {
		t.Fatalf("%s is in recovery, so it is not the primary", backendAddr())
	}
}

func directDSN(addr string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), addr, backendDB())
}

// dialDirect connects straight to a node, bypassing the proxy.
func dialDirect(t *testing.T, addr string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, directDSN(addr))
	if err != nil {
		t.Fatalf("connect directly to %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func inRecovery(t *testing.T, addr string) bool {
	t.Helper()
	conn := dialDirect(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var recovering bool
	if err := conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&recovering); err != nil {
		t.Fatalf("pg_is_in_recovery on %s: %v", addr, err)
	}
	return recovering
}

// walReceivers reports how many WAL receivers the node has, which is how a
// replica that has been cut off from its primary is distinguished from one that
// is merely idle.
func walReceivers(t *testing.T, addr string) int {
	t.Helper()
	conn := dialDirect(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_stat_wal_receiver").Scan(&n); err != nil {
		t.Fatalf("pg_stat_wal_receiver on %s: %v", addr, err)
	}
	return n
}

// startCluster runs a proxy configured with both nodes.
func startCluster(t *testing.T) *stack {
	t.Helper()
	requireCluster(t)

	return startStackWith(t, func(cfg string) string {
		return withReplicaBackend(cfg, replicaAddr())
	})
}

// withReplicaBackend appends the replica to the generated config's backend list.
func withReplicaBackend(cfg, replica string) string {
	const marker = "\ncache:\n"
	idx := strings.Index(cfg, marker)
	if idx < 0 {
		panic("harness config no longer has a cache block to anchor on")
	}

	entry := fmt.Sprintf(`  - addr: "%s"
    agent_addr: "127.0.0.1:19094"
    role: replica
    weight: 1
    zone: e2e
    admin_dsn: "%s"
`, replica, directDSN(replica))

	return cfg[:idx] + "\n" + entry + cfg[idx:]
}

// connectSimple opens a client that uses the simple query protocol.
//
// The routing tests want to know which backend answered, and the extended
// protocol currently cannot survive a pooled server connection being reused —
// see TestPreparedStatementSurvivesConnectionReuse. Using the simple protocol
// here keeps those tests measuring routing instead of re-failing on a defect
// that has nothing to do with routing.
func connectSimple(t *testing.T, ctx context.Context, s *stack) *pgx.Conn {
	t.Helper()

	cfg, err := pgx.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), s.proxyAddr, backendDB()))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect to the proxy: %v", err)
	}
	return conn
}

// containerRuntime returns the CLI that scripts/e2e-cluster.sh used, so a test
// can reach into a node the way an operator would.
func containerRuntime(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"docker", "podman", "container"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("no container runtime on PATH; cannot manipulate the cluster")
	return ""
}

func runtimeExec(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	cr := containerRuntime(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	full := append([]string{"exec", name}, args...)
	out, err := exec.CommandContext(ctx, cr, full...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func primaryContainer() string { return envOr("PONTUS_E2E_PRIMARY_NAME", "pontus-e2e-primary") }
func replicaContainer() string { return envOr("PONTUS_E2E_REPLICA_NAME", "pontus-e2e-replica") }

// psqlOn runs SQL as a superuser inside a node's container.
func psqlOn(t *testing.T, container, sql string) (string, error) {
	t.Helper()
	return runtimeExec(t, container, "psql", "-U", backendUser(), "-tAc", sql)
}

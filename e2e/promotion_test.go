//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Automatic promotion, end to end, against real PostgreSQL with a real agent.
//
// This is the one part of failover that cannot be faked: promotion runs
// `pg_ctl promote` through the agent sidecar on the database host, so nothing
// short of an agent inside the container exercises it. Until this existed, the
// entire promote path — agent address, token, system-info lookup, command
// dispatch, exit-code handling, and the pool noticing the role changed — ran
// only in unit tests against a mock provisioner.
//
// Promotion is **one way**. A promoted replica is on a new timeline and cannot
// go back to following its old primary, so this needs a cluster it is allowed
// to destroy. It refuses to run against the shared one:
//
//	AGENT=$(mktemp -d)/pontus-agent
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$AGENT" ./cmd/agent
//	AGENT_BINARY=$AGENT AGENT_TOKEN=e2e-agent-token \
//	  PRIMARY_NAME=pontus-promo-primary REPLICA_NAME=pontus-promo-replica \
//	  PRIMARY_PORT=55842 REPLICA_PORT=55843 \
//	  PRIMARY_AGENT_PORT=19191 REPLICA_AGENT_PORT=19193 \
//	  ./scripts/e2e-cluster.sh up
//	PONTUS_E2E_PROMO_PRIMARY=127.0.0.1:55842 \
//	  PONTUS_E2E_PROMO_REPLICA=127.0.0.1:55843 \
//	  PONTUS_E2E_PROMO_TOKEN=e2e-agent-token \
//	  PONTUS_E2E_PROMO_PRIMARY_CONTAINER=pontus-promo-primary \
//	  go test -tags=e2e ./e2e/ -run TestAutomaticPromotion -v
//
// then tear that pair down with the same PRIMARY_NAME/REPLICA_NAME and `down`.
func TestAutomaticPromotion(t *testing.T) {
	primary := os.Getenv("PONTUS_E2E_PROMO_PRIMARY")
	replica := os.Getenv("PONTUS_E2E_PROMO_REPLICA")
	token := os.Getenv("PONTUS_E2E_PROMO_TOKEN")
	container := os.Getenv("PONTUS_E2E_PROMO_PRIMARY_CONTAINER")

	if primary == "" || replica == "" || token == "" || container == "" {
		t.Skip("needs a disposable cluster with agents; see the comment above this test")
	}
	// Promotion cannot be undone, so refuse to point it at the shared pair even
	// if someone wires the variables up by mistake.
	if primary == backendAddr() || replica == replicaAddr() {
		t.Fatal("refusing to promote against the shared cluster; use a disposable pair")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	s := startStackWith(t, func(cfg string) string {
		return promotionConfig(cfg, primary, replica, token)
	})

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx,
		"CREATE TABLE IF NOT EXISTS promo (id serial primary key)"); err != nil {
		t.Fatalf("baseline write failed: %v", err)
	}

	if inRecovery(t, replica) != true {
		t.Fatal("the replica is not in recovery; there is nothing to promote")
	}

	rt := containerRuntime(t)
	containerDo(t, rt, "stop", container)
	waitReachable(t, primary, false, 60*time.Second)

	// Pontus has to notice the primary is gone, cross its failure threshold,
	// pick the replica, and reach the agent to promote it.
	deadline := time.Now().Add(150 * time.Second)
	promoted := false
	for time.Now().Before(deadline) {
		if pgReachable(replica) && !inRecovery(t, replica) {
			promoted = true
			break
		}
		time.Sleep(2 * time.Second)
	}

	if !promoted {
		t.Fatalf("the replica was never promoted after the primary went away; "+
			"proxy log:\n%s", tailLog(s.logs.String(), 3000))
	}
	t.Log("the replica left recovery — it was promoted")

	// Promotion is only half the job: the proxy has to start using it. Nothing
	// in the pool learns a role changed until its own deep check runs, so a
	// promotion the data plane never notices is a promotion that changed
	// nothing for clients.
	writeDeadline := time.Now().Add(120 * time.Second)
	var lastErr error
	for time.Now().Before(writeDeadline) {
		wctx, wcancel := context.WithTimeout(ctx, 20*time.Second)
		c, cerr := connectSimpleOrErr(wctx, s)
		if cerr == nil {
			_, lastErr = c.Exec(wctx, "INSERT INTO promo DEFAULT VALUES")
			_ = c.Close(context.Background())
		} else {
			lastErr = cerr
		}
		wcancel()
		if lastErr == nil {
			t.Log("writes reached the promoted node through the proxy")
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Errorf("the replica was promoted but writes never reached it through the "+
		"proxy: %v\nproxy log:\n%s", lastErr, tailLog(s.logs.String(), 3000))
}

// promotionConfig builds a two-backend config with agents and failover on.
func promotionConfig(cfg, primary, replica, token string) string {
	// Point the primary's entry at the disposable pair rather than the shared
	// backend the harness defaults to.
	cfg = strings.Replace(cfg, backendAddr(), primary, 1)

	cfg = strings.Replace(cfg,
		`    agent_addr: "127.0.0.1:19093"`,
		fmt.Sprintf("    agent_addr: \"127.0.0.1:19191\"\n    agent_token: %q", token), 1)

	const marker = "\ncache:\n"
	idx := strings.Index(cfg, marker)
	if idx < 0 {
		panic("harness config no longer has a cache block to anchor on")
	}
	entry := fmt.Sprintf(`  - addr: "%s"
    agent_addr: "127.0.0.1:19193"
    agent_token: %q
    role: replica
    weight: 1
    zone: e2e
    admin_dsn: "%s"
`, replica, token, directDSN(replica))
	cfg = cfg[:idx] + "\n" + entry + cfg[idx:]

	// Failover on, and quick to decide: the harness health interval is 2s, so
	// two consecutive failures is about four seconds of being sure rather than
	// the thirty a default deployment would prefer.
	cfg = strings.Replace(cfg, "  enabled: false\n  failure_threshold: 3",
		"  enabled: true\n  failure_threshold: 2", 1)
	return cfg
}

func tailLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

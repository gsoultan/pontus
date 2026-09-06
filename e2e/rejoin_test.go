//go:build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Automatic recovery after a real failover.
//
// A former primary that comes back is up, answers queries, and will never
// stream again on its own — it is on an abandoned timeline. `auto_reattach`
// keeps reads off it; nothing used to fix it, so the cluster ran permanently
// short until an operator noticed. This measures whether it comes back on its
// own.
//
// **This test fails today, and that is the point.** It is the acceptance test
// for automatic fallback, and the dependency it needs does not exist: the
// agent's SetupReplication (`agent/infrastructure/management.go`) is a stub
// that sleeps three times and reports "Replication configured" at 100% without
// running pg_basebackup, pg_rewind, or writing standby.signal. Every recovery
// path that rebuilds a node — split-brain self-healing, follow_primary and
// auto_rejoin — calls into it. Pontus now detects the lie and reports the
// rebuild as failed rather than believing it, so what this measures is the gap
// rather than a regression.
//
// It runs only when four environment variables are set deliberately, so it
// cannot redden a build that did not ask for it.
//
// Destructive twice over: it stops the primary, and promotion cannot be undone.
// It refuses the shared cluster and needs a disposable pair with agents, built
// exactly as TestAutomaticPromotion documents:
//
//	AGENT=$(mktemp -d)/pontus-agent
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$AGENT" ./cmd/agent
//	AGENT_BINARY=$AGENT AGENT_TOKEN=e2e-agent-token \
//	  PRIMARY_NAME=pontus-promo-primary REPLICA_NAME=pontus-promo-replica \
//	  PRIMARY_PORT=55842 REPLICA_PORT=55843 \
//	  PRIMARY_AGENT_PORT=19191 REPLICA_AGENT_PORT=19193 \
//	  ./scripts/e2e-cluster.sh up
//	PONTUS_E2E_PRIMARY_NAME=pontus-promo-primary \
//	  PONTUS_E2E_REPLICA_NAME=pontus-promo-replica \
//	  PONTUS_E2E_PROMO_PRIMARY=127.0.0.1:55842 \
//	  PONTUS_E2E_PROMO_REPLICA=127.0.0.1:55843 \
//	  PONTUS_E2E_PROMO_TOKEN=e2e-agent-token \
//	  PONTUS_E2E_PROMO_PRIMARY_CONTAINER=pontus-promo-primary \
//	  go test -tags=e2e ./e2e/ -run TestAutomaticRejoin -v
func TestAutomaticRejoinReturnsTheOldPrimary(t *testing.T) {
	primary := os.Getenv("PONTUS_E2E_PROMO_PRIMARY")
	replica := os.Getenv("PONTUS_E2E_PROMO_REPLICA")
	token := os.Getenv("PONTUS_E2E_PROMO_TOKEN")
	container := os.Getenv("PONTUS_E2E_PROMO_PRIMARY_CONTAINER")

	if primary == "" || replica == "" || token == "" || container == "" {
		t.Skip("needs a disposable cluster with agents; see the comment above this test")
	}
	if primary == backendAddr() || replica == replicaAddr() {
		t.Fatal("refusing to fail over the shared cluster; use a disposable pair")
	}

	if !inRecovery(t, replica) {
		t.Fatal("the replica is not in recovery; there is nothing to promote")
	}

	s := startStackWith(t, func(cfg string) string {
		return rejoinConfig(promotionConfig(cfg, primary, replica, token))
	})

	rt := containerRuntime(t)

	// 1. Take the primary away and let Pontus promote the replica.
	containerDo(t, rt, "stop", container)
	waitReachable(t, primary, false, 60*time.Second)

	if !waitFor(120*time.Second, func() bool {
		recovering, err := recoveryState(replica)
		return err == nil && !recovering
	}) {
		t.Fatalf("the replica was never promoted\nproxy log:\n%s", tailLog(s.logs.String(), 3000))
	}
	t.Log("the replica was promoted")

	// 2. The old primary comes back. It still believes it is the primary, and
	//    it is on a timeline the new primary abandoned.
	containerDo(t, rt, "start", container)
	waitServing(t, primary, 120*time.Second)
	t.Log("the old primary is back and serving")

	// 3. Nobody intervenes. The cluster should return to one primary and one
	//    streaming replica on its own.
	recovered := waitFor(300*time.Second, func() bool {
		recovering, err := recoveryState(primary)
		if err != nil || !recovering {
			return false
		}
		receivers, err := receiverCount(primary)
		return err == nil && receivers > 0
	})
	if !recovered {
		recovering, _ := recoveryState(primary)
		receivers, _ := receiverCount(primary)
		t.Fatalf("the old primary never rejoined: in_recovery=%v receivers=%d\nproxy log:\n%s",
			recovering, receivers, tailLog(s.logs.String(), 6000))
	}

	// The write role stays where the failover put it. Returning it to a
	// recovered node is a second unplanned outage and remains an operator's
	// call — a rejoin that quietly moved it back would be the bug this whole
	// path exists to avoid.
	if recovering, err := recoveryState(replica); err == nil && recovering {
		t.Error("the promoted node went back into recovery; the write role moved on its own")
	}

	t.Log("the old primary rejoined as a streaming replica, and the write role did not move")
}

// recoveryState and receiverCount are the polling counterparts of inRecovery
// and walReceivers.
//
// Two differences matter. They close the connection immediately rather than at
// the end of the test — dialDirect defers its close to t.Cleanup, which is
// right for a one-shot assertion and exhausts max_connections when polled once
// a second. And they return an error instead of failing: a node that is
// restarting is unreachable for a while, which is a state to wait through
// rather than to fail on.
func recoveryState(addr string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, directDSN(addr))
	if err != nil {
		return false, err
	}
	defer conn.Close(context.Background())

	var recovering bool
	err = conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&recovering)
	return recovering, err
}

func receiverCount(addr string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, directDSN(addr))
	if err != nil {
		return 0, err
	}
	defer conn.Close(context.Background())

	var n int
	err = conn.QueryRow(ctx, "SELECT count(*) FROM pg_stat_wal_receiver").Scan(&n)
	return n, err
}

// waitFor polls until cond holds or the budget runs out.
func waitFor(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// rejoinConfig turns automatic rejoin on and makes it quick enough to observe.
//
// The production defaults are five minutes between attempts and thirty for one
// rebuild, which are right for a base backup of a real cluster and far longer
// than a test should wait.
func rejoinConfig(cfg string) string {
	const marker = "  auto_reattach: true"
	if !strings.Contains(cfg, marker) {
		panic("harness config no longer has auto_reattach to anchor on")
	}
	return strings.Replace(cfg, marker, marker+`
  auto_rejoin: true
  auto_rejoin_interval: 5s
  auto_rejoin_timeout: 120s
  auto_rejoin_max_attempts: 5`, 1)
}

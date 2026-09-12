//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Automatic fallback, measured end to end.
//
// Two real PostgreSQL clusters, real streaming replication, a real agent per
// node, and no operator: kill the primary, let Pontus promote the standby,
// bring the old primary back, and see whether the cluster returns to a primary
// and a streaming replica on its own.
//
// Every earlier version of this ran against the container harness, where
// PostgreSQL is PID 1 and stopping it takes the agent down mid-rebuild. Here
// the agent is its own process, which is the VM or systemd shape it is built
// for.
func TestLocalAutomaticFallback(t *testing.T) {
	c := startLocalCluster(t)
	s := c.startPontus(t)

	old := c.primary
	promoted := c.standby

	// A baseline write, so the cluster has something to diverge over.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := proxySession(t, ctx, s)
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS fallback (id serial primary key)"); err != nil {
		t.Fatalf("baseline write: %v", err)
	}
	conn.Close(context.Background())

	// 1. The primary dies.
	c.stopNode(old)
	if !waitFor(60*time.Second, func() bool { return !pgReachable(old.addr()) }) {
		t.Fatal("the primary is still reachable after being stopped")
	}

	// 2. Pontus promotes the standby.
	if !waitFor(120*time.Second, func() bool {
		recovering, err := queryBool(promoted.dsn(), "SELECT pg_is_in_recovery()")
		return err == nil && !recovering
	}) {
		t.Fatalf("the standby was never promoted\nproxy log:\n%s", tailLog(s.logs.String(), 4000))
	}
	t.Log("the standby was promoted")

	// 3. The old primary comes back. It still believes it is the primary, and
	//    it is on a timeline the new primary abandoned.
	c.start(old)
	c.waitServing(old, 120*time.Second)
	t.Log("the old primary is back and serving")

	// 4. Nobody intervenes.
	recovered := waitFor(300*time.Second, func() bool {
		recovering, err := queryBool(old.dsn(), "SELECT pg_is_in_recovery()")
		if err != nil || !recovering {
			return false
		}
		receivers, err := queryInt(old.dsn(), "SELECT count(*) FROM pg_stat_wal_receiver")
		return err == nil && receivers > 0
	})
	if !recovered {
		recovering, _ := queryBool(old.dsn(), "SELECT pg_is_in_recovery()")
		receivers, _ := queryInt(old.dsn(), "SELECT count(*) FROM pg_stat_wal_receiver")
		t.Fatalf("the old primary never rejoined: in_recovery=%v receivers=%d\nproxy log:\n%s\nagent log:\n%s",
			recovering, receivers, tailLog(s.logs.String(), 5000), readLog(old.agentLog))
	}
	t.Log("the old primary rejoined as a streaming replica, with no operator")

	// The write role stays where the failover put it. A rejoin that quietly
	// moved it back would be the second unplanned outage this whole path exists
	// to avoid.
	if recovering, err := queryBool(promoted.dsn(), "SELECT pg_is_in_recovery()"); err == nil && recovering {
		t.Error("the promoted node went back into recovery; the write role moved on its own")
	}

	// And the rebuilt node really follows the new primary, rather than merely
	// being in recovery against nothing.
	if !waitFor(60*time.Second, func() bool {
		state, err := queryString(promoted.dsn(), "SELECT state FROM pg_stat_replication LIMIT 1")
		return err == nil && state == "streaming"
	}) {
		t.Error("the new primary reports no streaming replica, so the rebuilt node is not following it")
	}

	// The cluster is whole: a write through the proxy still works.
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer writeCancel()

	after := proxySession(t, writeCtx, s)
	defer after.Close(context.Background())
	if _, err := after.Exec(writeCtx, "INSERT INTO fallback DEFAULT VALUES"); err != nil {
		t.Errorf("writing through the proxy after recovery: %v", err)
	}
}

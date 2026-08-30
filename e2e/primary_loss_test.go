//go:build e2e

package e2e

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// What clients see when the primary goes away.
//
// These stop the primary container, so they are opt-in: a shared cluster that
// one of these leaves half-down fails every test that runs after it. Set
// PONTUS_E2E_DISRUPTIVE=1 to run them.
//
//	PONTUS_E2E_DISRUPTIVE=1 go test -tags=e2e ./e2e/ -run TestPrimaryLoss
//
// Automatic *promotion* is not covered here and cannot be: it runs `pg_ctl
// promote` through the agent sidecar on the database host, and these containers
// are plain postgres images with no agent in them. What is covered is the part
// every client feels — reads continuing, writes not hanging, and recovery
// without restarting the proxy.
func requireDisruptive(t *testing.T) {
	t.Helper()
	if os.Getenv("PONTUS_E2E_DISRUPTIVE") != "1" {
		t.Skip("stops the primary container; set PONTUS_E2E_DISRUPTIVE=1 to run")
	}
}

// containerDo runs a lifecycle command with a runtime resolved earlier.
//
// The runtime has to be captured while the container is still *running*:
// containerRuntime identifies it by exec-ing inside, which a stopped container
// cannot answer. Resolving it lazily meant the cleanup that restarts the
// primary could not find a runtime and left the cluster down — which then
// failed every test that ran afterwards.
func containerDo(t *testing.T, rt, action, name string) {
	t.Helper()
	out, err := exec.Command(rt, action, name).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s %s failed: %v\n%s", rt, action, name, err, out)
	}
}

// pgReachable reports whether a Postgres is accepting connections.
func pgReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// waitReachable waits for a Postgres to answer, or not to.
func waitReachable(t *testing.T, addr string, want bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if pgReachable(addr) == want {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s reachable=%v never became %v within %v", addr, !want, want, within)
}

// What survives a primary outage, measured on both kinds of session.
//
// An established session and a brand new one are different paths: the first
// already holds a backend connection, the second has to acquire one for its
// handshake — and that acquisition is write-hinted, because nothing is known
// about a session before it has spoken.
func TestPrimaryLossReadsKeepWorking(t *testing.T) {
	requireCluster(t)
	requireDisruptive(t)

	s := startCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	// Established before the outage.
	established := connectSimple(t, ctx, s)
	defer established.Close(context.Background())

	var n int
	if err := established.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("baseline read failed: %v", err)
	}

	rt := containerRuntime(t)
	t.Cleanup(func() { restorePrimary(t, rt) })
	containerDo(t, rt, "stop", primaryContainer())
	waitReachable(t, backendAddr(), false, 60*time.Second)

	// Give Pontus a few health intervals to notice.
	time.Sleep(6 * time.Second)

	t.Run("established session", func(t *testing.T) {
		readCtx, readCancel := context.WithTimeout(ctx, 60*time.Second)
		defer readCancel()

		var got int
		err := established.QueryRow(readCtx, "SELECT 1").Scan(&got)
		if err != nil {
			t.Errorf("a session that was already connected could not read with the "+
				"primary down and a healthy replica present: %v", err)
		} else if got != 1 {
			t.Errorf("read returned %d", got)
		}
	})

	t.Run("new session", func(t *testing.T) {
		// A new session has to acquire a connection for its handshake, and that
		// acquisition asks for a *write* backend because nothing is known about
		// the session yet. With no primary there is nothing to give it, so a
		// read-only client cannot connect at all while the primary is down —
		// which turns a primary outage into a total outage for a read-heavy
		// deployment that has replicas precisely to avoid one.
		//
		// Asserted as it behaves rather than as it ought to: pinning it here
		// means a change either way is deliberate and visible. What must not
		// regress is the *bound* — failing is a decision, hanging is not.
		connCtx, connCancel := context.WithTimeout(ctx, 90*time.Second)
		defer connCancel()

		start := time.Now()
		conn, err := connectSimpleOrErr(connCtx, s)
		elapsed := time.Since(start)

		if err == nil {
			defer conn.Close(context.Background())
			var got int
			if rerr := conn.QueryRow(connCtx, "SELECT 1").Scan(&got); rerr != nil {
				t.Errorf("a new session connected but could not read: %v", rerr)
			}
			t.Logf("a new session connected in %v with the primary down",
				elapsed.Round(time.Millisecond))
			return
		}

		t.Logf("KNOWN: a new session cannot be opened while the primary is down "+
			"(%v): %v", elapsed.Round(time.Second), err)
		if elapsed > 120*time.Second {
			t.Errorf("connecting with no primary took %v to fail; nothing bounded it",
				elapsed.Round(time.Second))
		}
	})
}

// A write with nowhere to go must fail, and fail in bounded time. Hanging until
// something else gives up is the failure mode waitWhilePaused exists to
// prevent: with no replica to promote the pause cannot succeed, so the client
// has to get its own answer rather than wait out the failover's.
func TestPrimaryLossWritesFailFastAndRecover(t *testing.T) {
	requireCluster(t)
	requireDisruptive(t)

	s := startCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx,
		"CREATE TABLE IF NOT EXISTS primary_loss (id serial primary key, note text)"); err != nil {
		t.Fatalf("setup write failed: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer ccancel()
		c, err := connectSimpleOrErr(cctx, s)
		if err == nil {
			_, _ = c.Exec(cctx, "DROP TABLE IF EXISTS primary_loss")
			_ = c.Close(context.Background())
		}
	})

	rt := containerRuntime(t)
	t.Cleanup(func() { restorePrimary(t, rt) })
	containerDo(t, rt, "stop", primaryContainer())
	waitReachable(t, backendAddr(), false, 60*time.Second)
	time.Sleep(6 * time.Second)

	writeCtx, writeCancel := context.WithTimeout(ctx, 120*time.Second)
	defer writeCancel()

	start := time.Now()
	_, err := conn.Exec(writeCtx, "INSERT INTO primary_loss (note) VALUES ('during outage')")
	elapsed := time.Since(start)

	if err == nil {
		t.Error("a write succeeded with no primary; it went somewhere it should not have")
	}
	if elapsed > 90*time.Second {
		t.Errorf("a write with no primary took %v to fail; nothing bounded it",
			elapsed.Round(time.Second))
	} else {
		t.Logf("write refused after %v: %v", elapsed.Round(time.Millisecond), err)
	}

	// Recovery, without restarting the proxy.
	restorePrimary(t, rt)
	waitReachable(t, backendAddr(), true, 90*time.Second)

	deadline := time.Now().Add(120 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		rctx, rcancel := context.WithTimeout(ctx, 20*time.Second)
		c, cerr := connectSimpleOrErr(rctx, s)
		if cerr == nil {
			_, lastErr = c.Exec(rctx, "INSERT INTO primary_loss (note) VALUES ('after recovery')")
			_ = c.Close(context.Background())
		} else {
			lastErr = cerr
		}
		rcancel()
		if lastErr == nil {
			t.Logf("writes resumed after the primary returned")
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Errorf("writes never resumed after the primary came back: %v", lastErr)
}

// restorePrimary brings the primary back and waits for it to answer. Safe to
// call twice — the test calls it explicitly and the cleanup calls it again.
func restorePrimary(t *testing.T, rt string) {
	t.Helper()
	out, err := exec.Command(rt, "start", primaryContainer()).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already") {
		t.Logf("could not restart the primary (%v): %s", err, out)
	}
}

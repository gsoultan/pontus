package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// waitForDemote waits for the rebuild goroutines to record their work. Rejoin
// runs off the monitor tick on purpose — a base backup must not block the loop
// that also detects failover — so a test has to wait for it rather than read
// straight after the call.
func waitForDemote(t *testing.T, p *mockProvisioner, want int) []string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		got := append([]string(nil), p.demoted...)
		p.mu.Unlock()
		if len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if want > len(p.demoted) {
		t.Fatalf("timed out waiting for %d rebuilds, saw %v", want, p.demoted)
	}
	return append([]string(nil), p.demoted...)
}

func demoteCount(p *mockProvisioner) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.demoted)
}

// Reachability is the load-bearing half. A node Pontus cannot reach might be
// rebooting, and rebuilding it is both impossible — the rebuild runs through its
// agent — and destructive if it were.
func TestNeedsRejoinSelectsOnlyReachableNonStreamingReplicas(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend *mockBackend
		want    bool
	}{
		{
			"a replica that is up and no longer streaming",
			&mockBackend{address: "r1", role: pool.RoleReplica, healthy: true, notStreaming: true},
			true,
		},
		{
			"a replica that is streaming normally",
			&mockBackend{address: "r1", role: pool.RoleReplica, healthy: true},
			false,
		},
		{
			"a replica Pontus cannot reach, which may only be rebooting",
			&mockBackend{address: "r1", role: pool.RoleReplica, healthy: false, notStreaming: true},
			false,
		},
		{
			"the primary itself",
			&mockBackend{address: "p1", role: pool.RolePrimary, healthy: true, notStreaming: true},
			false,
		},
		{
			"a node an operator is draining",
			&mockBackend{address: "r1", role: pool.RoleReplica, healthy: true, notStreaming: true, draining: true},
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsRejoin(tc.backend, "p1"); got != tc.want {
				t.Errorf("needsRejoin = %v, want %v", got, tc.want)
			}
		})
	}
}

// The node the primary runs on is never a rebuild target, whatever it reports.
func TestNeedsRejoinNeverSelectsTheCurrentPrimary(t *testing.T) {
	// Even a backend still labelled a replica is left alone if it is the
	// address currently serving writes, because rebuilding it would discard
	// the cluster.
	b := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}
	if needsRejoin(b, "p1") {
		t.Error("the current primary was selected for a rebuild")
	}
}

// The gap this closes: demotion is a single attempt, logged if it fails and
// never retried, so a former primary back on an abandoned timeline stays out of
// the cluster until a person notices.
func TestRejoinRebuildsANonStreamingNode(t *testing.T) {
	primary := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	stranded := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}
	fine := &mockBackend{address: "r2", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{primary, stranded, fine}
	provisioner := &mockProvisioner{}
	// The rebuild works, so the node comes back streaming. Confirmation reads
	// that, rather than the agent's word for it.
	provisioner.onDemote = func(addr string) {
		if addr == "p1" {
			stranded.setStreaming(true)
		}
	}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, AutoRejoin: true, AutoRejoinMaxAttempts: 3})

	mgr.reconcileRejoins(context.Background(), "r1", backends)

	demoted := waitForDemote(t, provisioner, 1)
	if len(demoted) != 1 || demoted[0] != "p1" {
		t.Fatalf("rebuilt %v, want only p1", demoted)
	}

	provisioner.mu.Lock()
	target := provisioner.repointed["p1"]
	provisioner.mu.Unlock()
	if target != "r1" {
		t.Errorf("p1 was pointed at %q, want the current primary r1", target)
	}

	// The rebuild happened on the database host, so the pool still believes
	// what it last measured until it is told to look again.
	if stranded.reevaluations() == 0 {
		t.Error("the rebuilt node's role was never re-evaluated")
	}
}

// Off by default, because a rebuild can mean a pg_basebackup that discards a
// data directory.
func TestRejoinDoesNothingWhenDisabled(t *testing.T) {
	primary := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	stranded := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}

	backends := []pool.Backend{primary, stranded}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true})

	mgr.reconcileRejoins(context.Background(), "r1", backends)
	time.Sleep(50 * time.Millisecond)

	if n := demoteCount(provisioner); n != 0 {
		t.Errorf("a node was rebuilt with auto_rejoin off: %d attempts", n)
	}
}

// A node that fails three rebuilds is not going to pass the fourth, and
// retrying past that turns a broken replica into a permanent base backup
// against a healthy primary.
func TestRejoinStopsAfterItsAttemptBudget(t *testing.T) {
	primary := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	broken := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}

	backends := []pool.Backend{primary, broken}
	provisioner := &mockProvisioner{failDemoteFor: "p1"}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{
			Enabled: true, AutoRejoin: true,
			AutoRejoinMaxAttempts: 2,
			// No throttle, so the budget is what stops it rather than the clock.
			AutoRejoinInterval: time.Nanosecond,
		})

	// Far more ticks than the budget allows.
	for range 10 {
		mgr.reconcileRejoins(context.Background(), "r1", backends)
		time.Sleep(5 * time.Millisecond)
	}

	// The provisioner records only successes, so count the attempts the tracker
	// allowed instead.
	mgr.rejoins.mu.Lock()
	attempts := mgr.rejoins.nodes["p1"].attempts
	mgr.rejoins.mu.Unlock()

	if attempts != 2 {
		t.Errorf("made %d attempts, want the budget of 2", attempts)
	}
}

// A node that cannot be rebuilt must not be rebuilt continuously.
func TestRejoinThrottlesRetries(t *testing.T) {
	primary := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	broken := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}

	backends := []pool.Backend{primary, broken}
	provisioner := &mockProvisioner{failDemoteFor: "p1"}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{
			Enabled: true, AutoRejoin: true,
			AutoRejoinMaxAttempts: 10,
			AutoRejoinInterval:    time.Hour,
		})

	for range 5 {
		mgr.reconcileRejoins(context.Background(), "r1", backends)
		time.Sleep(5 * time.Millisecond)
	}

	mgr.rejoins.mu.Lock()
	attempts := mgr.rejoins.nodes["p1"].attempts
	mgr.rejoins.mu.Unlock()

	if attempts != 1 {
		t.Errorf("made %d attempts within one interval, want 1", attempts)
	}
}

// A node that recovers — however it recovered — starts from a clean budget. An
// operator who rebuilds by hand has resolved the problem, and the next outage
// should not inherit a count from the last one.
func TestRejoinForgetsANodeThatRecovers(t *testing.T) {
	primary := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	flapping := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}

	backends := []pool.Backend{primary, flapping}
	provisioner := &mockProvisioner{failDemoteFor: "p1"}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{
			Enabled: true, AutoRejoin: true,
			AutoRejoinMaxAttempts: 2,
			AutoRejoinInterval:    time.Nanosecond,
		})

	for range 4 {
		mgr.reconcileRejoins(context.Background(), "r1", backends)
		time.Sleep(5 * time.Millisecond)
	}

	mgr.rejoins.mu.Lock()
	_, tracked := mgr.rejoins.nodes["p1"]
	mgr.rejoins.mu.Unlock()
	if !tracked {
		t.Fatal("the failing node was not tracked")
	}

	// It comes back on its own.
	flapping.setStreaming(true)
	mgr.reconcileRejoins(context.Background(), "r1", backends)

	mgr.rejoins.mu.Lock()
	_, stillTracked := mgr.rejoins.nodes["p1"]
	mgr.rejoins.mu.Unlock()
	if stillTracked {
		t.Error("a recovered node kept its attempt history, so its next outage starts short of budget")
	}
}

// A successful rebuild clears the history too, so a node that fails again later
// gets the full budget rather than inheriting a count from an outage that is
// over.
func TestRejoinResetsAfterASuccess(t *testing.T) {
	primary := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	stranded := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}

	backends := []pool.Backend{primary, stranded}
	provisioner := &mockProvisioner{}
	provisioner.onDemote = func(addr string) {
		if addr == "p1" {
			stranded.setStreaming(true)
		}
	}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, AutoRejoin: true, AutoRejoinMaxAttempts: 2})

	mgr.reconcileRejoins(context.Background(), "r1", backends)
	waitForDemote(t, provisioner, 1)
	// The tracker clears only once confirmation passes.
	time.Sleep(100 * time.Millisecond)

	mgr.rejoins.mu.Lock()
	_, tracked := mgr.rejoins.nodes["p1"]
	mgr.rejoins.mu.Unlock()
	if tracked {
		t.Error("a successfully rebuilt node kept its attempt history")
	}
}

// Two ticks arriving while a rebuild is running must not start a second one:
// a base backup takes minutes and running two against one node is how a repair
// becomes an outage.
func TestRejoinDoesNotStartTwiceForOneNode(t *testing.T) {
	tracker := newRejoinTracker()

	ok, _ := tracker.begin("p1", time.Nanosecond, 5, time.Now())
	if !ok {
		t.Fatal("the first attempt was refused")
	}
	if again, _ := tracker.begin("p1", time.Nanosecond, 5, time.Now()); again {
		t.Error("a second rebuild started while the first was still running")
	}

	tracker.finish("p1", false)
	if resumed, _ := tracker.begin("p1", time.Nanosecond, 5, time.Now()); !resumed {
		t.Error("a retry was refused after the previous attempt finished")
	}
}

func TestRejoinTrackerReportsExhaustion(t *testing.T) {
	tracker := newRejoinTracker()

	for i := range 2 {
		ok, exhausted := tracker.begin("p1", time.Nanosecond, 2, time.Now())
		if !ok || exhausted {
			t.Fatalf("attempt %d refused: ok=%v exhausted=%v", i+1, ok, exhausted)
		}
		tracker.finish("p1", false)
	}

	ok, exhausted := tracker.begin("p1", time.Nanosecond, 2, time.Now())
	if ok {
		t.Error("an attempt was allowed past the budget")
	}
	if !exhausted {
		t.Error("running out of budget was not reported as exhaustion")
	}
}

// The agent's success is a claim about work Pontus cannot see, and the shipped
// agent's SetupReplication is a stub that reports "Replication configured"
// having done nothing. Believing it marks a node recovered, clears its attempt
// history, and reports the cluster healthy while it serves nothing.
func TestRejoinDoesNotBelieveAnAgentThatDidNothing(t *testing.T) {
	primary := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}
	// The provisioner returns success, but the node never starts streaming.
	stranded := &mockBackend{address: "p1", role: pool.RoleReplica, healthy: true, notStreaming: true}

	backends := []pool.Backend{primary, stranded}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, AutoRejoin: true, AutoRejoinMaxAttempts: 3})

	mgr.reconcileRejoins(context.Background(), "r1", backends)
	waitForDemote(t, provisioner, 1)

	// Confirmation has to time out before the attempt is recorded as failed.
	deadline := time.Now().Add(rejoinConfirmTimeout + 5*time.Second)
	for time.Now().Before(deadline) {
		mgr.rejoins.mu.Lock()
		state, tracked := mgr.rejoins.nodes["p1"]
		done := tracked && !state.inFlight
		mgr.rejoins.mu.Unlock()
		if done {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("a rebuild that left the node not streaming was recorded as a success")
}

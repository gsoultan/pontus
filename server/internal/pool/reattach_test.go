package pool

import (
	"testing"
	"time"
)

// A replica that has lost its WAL receiver is the dangerous case: up, healthy,
// answering, and every row it returns is older than the last. It must be kept
// out of the read pool.
func TestNonStreamingReplicaIsNotReadEligible(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(true, time.Millisecond)

	p := &Server{role: RoleReplica}
	p.setReplicating(false)

	if p.IsReplicating() {
		t.Error("a replica with no WAL receiver was considered fit to serve reads")
	}
}

// A primary is always eligible; it has no upstream to stream from.
func TestPrimaryIsAlwaysReplicating(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(true, time.Hour)

	p := &Server{role: RolePrimary}
	p.setReplicating(false)

	if !p.IsReplicating() {
		t.Error("a primary was excluded from routing for not streaming")
	}
}

// Re-admission after a drop waits out the dwell interval. A node that
// reconnects, catches a few segments and drops again is worse than one that
// stays out: each brief reappearance pulls traffic onto data that is about to
// go stale again.
func TestReattachWaitsOutTheDwellInterval(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(true, time.Hour)

	p := &Server{role: RoleReplica}
	p.setReplicating(true)  // healthy at first
	p.setReplicating(false) // loses its receiver
	p.setReplicating(true)  // and comes back

	if p.IsReplicating() {
		t.Error("re-admitted the instant it reconnected, with an hour of dwell configured")
	}
}

// A replica that is already streaming when Pontus first sees it must be usable
// straight away.
//
// The dwell is a penalty for recovering from a drop, not for being newly
// observed. Charging it on first sight took every replica out of the read pool
// for a full interval after each restart and each config reload — the proxy
// booting is not evidence that a replica streaming for a week is unwell. Found
// end to end: no read ever reached a real replica in the first minute.
func TestReplicaStreamingOnFirstSightIsUsableImmediately(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(true, time.Hour)

	p := &Server{role: RoleReplica}
	p.setReplicating(true)

	if !p.IsReplicating() {
		t.Error("a replica already streaming at startup was held out of the read pool " +
			"for the dwell interval")
	}
}

func TestReattachAdmitsAfterTheDwellElapses(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(true, time.Millisecond)

	p := &Server{role: RoleReplica}
	p.setReplicating(true)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.IsReplicating() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Error("never re-admitted after the dwell interval elapsed")
}

// Continued streaming must not keep pushing the clock forward, or the dwell
// would never elapse and the replica would never come back.
func TestDwellClockStartsOnceNotOnEveryCheck(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(true, time.Millisecond)

	p := &Server{role: RoleReplica}
	p.setReplicating(true)
	first := p.replicatingSince.Load()

	time.Sleep(5 * time.Millisecond)
	p.setReplicating(true)

	if p.replicatingSince.Load() != first {
		t.Error("a later observation restarted the dwell clock; the replica would never be re-admitted")
	}
	if !p.IsReplicating() {
		t.Error("not re-admitted despite the dwell having elapsed")
	}
}

// Dropping out must reset the clock, so a flapping node starts the wait again.
func TestLosingTheReceiverResetsTheDwell(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(true, time.Millisecond)

	p := &Server{role: RoleReplica}
	p.setReplicating(true)
	time.Sleep(5 * time.Millisecond)
	if !p.IsReplicating() {
		t.Fatal("setup: expected the replica to be eligible")
	}

	p.setReplicating(false)
	if p.IsReplicating() {
		t.Error("still eligible after losing the WAL receiver")
	}
	if p.replicatingSince.Load() != 0 {
		t.Error("dwell clock was not reset")
	}
}

// With the policy off, routing ignores streaming state entirely.
func TestPolicyOffIgnoresStreamingState(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })
	SetAutoReattach(false, time.Hour)

	p := &Server{role: RoleReplica}
	p.setReplicating(false)

	if !p.IsReplicating() {
		t.Error("streaming state was still enforced with auto_reattach off")
	}
}

func TestNonPositiveDwellRestoresTheDefault(t *testing.T) {
	t.Cleanup(func() { SetAutoReattach(true, DefaultReattachInterval) })

	SetAutoReattach(true, 0)
	if _, dwell := AutoReattachPolicy(); dwell != DefaultReattachInterval {
		t.Errorf("dwell = %v after setting 0, want the default", dwell)
	}
}

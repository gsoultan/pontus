package proxy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// A writer paused for a failover has to wake when that failover resolves.
//
// sync.Cond only guarantees that if the state change and the Broadcast happen
// under the same lock the waiter holds. A broadcast that lands in the window
// between a waiter's "am I still paused?" check and its Wait() is simply lost —
// the waiter then sleeps until some *later* broadcast, and after a failover has
// resolved there is no later broadcast. The client hangs.
// A writer paused for a failover has to wake when that failover resolves.
//
// sync.Cond only guarantees that if the state change and the Broadcast happen
// under the lock the waiter holds. A broadcast landing in the window between a
// waiter's "am I still paused?" check and its Wait() is simply dropped: Wait
// registers on the notify list *after* the broadcast has already walked it. The
// waiter then sleeps until some later broadcast — and once a failover has
// resolved there is no later broadcast, so the client hangs for the life of its
// connection.
//
// The window is a few instructions wide. Racing it from outside does not
// reproduce it — 16000 attempts with a swept delay found nothing — so the test
// steps into it instead.
func TestPausedWritersWakeWhenTheFailoverResolves(t *testing.T) {
	g := newPauseHarness()
	g.inFailover.Store(true)

	// Start a real resolver from inside the window, exactly once.
	//
	// It has to run on its own goroutine: the waiter holds the read lock here,
	// and a resolver that takes the write lock — which is the fix — would
	// deadlock against itself if called inline. The sleep gives it time to
	// reach that lock while the waiter is still in the window, which is the
	// interleaving under test.
	var once sync.Once
	beforeWait = func() {
		once.Do(func() {
			go g.resolveFailover()
			time.Sleep(100 * time.Millisecond)
		})
	}
	t.Cleanup(func() { beforeWait = nil })

	// Generous: this is measuring "does it ever wake", not how fast.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		err := g.waitWhilePaused(ctx)
		done <- result{err, time.Since(start)}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Errorf("the failover resolved but the writer returned %v", got.err)
		}
		// The point of the pause is to ride out a promotion and then get on
		// with it. Waking only when the caller's own deadline expires is not
		// riding it out — with the default query_timeout that is a thirty
		// second stall on every write that was in flight when the primary was
		// promoted, after the new primary was already serving.
		if got.elapsed > time.Second {
			t.Errorf("the writer resumed %v after the failover resolved; it slept "+
				"through the broadcast and woke on its own deadline instead",
				got.elapsed.Round(time.Millisecond))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the writer never woke after the failover resolved")
	}
}

// The context still has to win when the failover does *not* resolve — that is
// what this wait exists for. A single backend has no replica to promote, so the
// failover cannot succeed and every writer must fail on its own deadline
// instead of the failover's.
func TestPausedWriterHonoursItsOwnDeadline(t *testing.T) {
	g := newPauseHarness()
	g.inFailover.Store(true) // never resolved

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := g.waitWhilePaused(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waited on an unresolved failover and got %v, want a deadline", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v to honour a 300ms deadline", elapsed)
	}
}

// A gateway not in failover must not touch the condition at all.
func TestUnpausedWriterDoesNotWait(t *testing.T) {
	g := newPauseHarness()

	start := time.Now()
	if err := g.waitWhilePaused(t.Context()); err != nil {
		t.Fatalf("an unpaused writer got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("an unpaused writer took %v", elapsed)
	}
}

// newPauseHarness builds only the failover machinery, so these tests exercise
// the wait rather than a whole gateway.
func newPauseHarness() *Gateway {
	g := new(Gateway)
	g.pauseCond = sync.NewCond(g.failoverMu.RLocker())
	return g
}

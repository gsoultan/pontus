package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsoultan/pontus/pkg/listen"
	orchestration2 "github.com/gsoultan/pontus/server/internal/orchestration"
)

// The regression this file exists for.
//
// claimOrchestration ran once, in NewRegistry's struct literal. When it failed
// it installed a permanent `func() bool { return false }` and returned. Nothing
// retried — while both log lines told the operator "this process takes over
// when the holder exits". So after every reuse_port upgrade, which is the only
// reason that option exists, the surviving process ran no failover, no
// follow-primary and no rejoin, permanently and silently.
func TestOrchestrationIsTakenOverWhenTheHolderExits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration.lock")

	// Stand in for the process being replaced.
	holder, err := listen.AcquireOrchestration(path)
	if err != nil {
		t.Fatalf("the stand-in holder could not claim the lock: %v", err)
	}

	// Restore the package-global predicate whatever happens.
	t.Cleanup(func() { orchestration2.SetOwnership(nil) })
	orchestration2.SetOwnership(func() bool { return false })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &Registry{}
	go r.awaitOrchestration(ctx, path)

	// While the holder is up, nothing may be taken.
	time.Sleep(2 * orchestrationRetryInterval)
	r.mu.RLock()
	taken := r.orchestrationLock
	r.mu.RUnlock()
	if taken != nil {
		t.Fatal("orchestration was taken while another process still held it")
	}

	// The holder exits, as a deploy would have it.
	if err := holder.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	deadline := time.Now().Add(4 * orchestrationRetryInterval)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		taken = r.orchestrationLock
		r.mu.RUnlock()
		if taken != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if taken == nil {
		t.Fatal("orchestration was never taken over after the holder exited; " +
			"this deployment has no failover manager")
	}
	t.Cleanup(func() { taken.Release() })
}

// The watcher must not outlive the registry. A goroutine polling a lock file
// for the life of the process is the `conc` veto in AGENTS.md.
func TestAwaitOrchestrationStopsWithTheContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration.lock")

	holder, err := listen.AcquireOrchestration(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Release() })

	ctx, cancel := context.WithCancel(context.Background())
	r := &Registry{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.awaitOrchestration(ctx, path)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * orchestrationRetryInterval):
		t.Fatal("awaitOrchestration did not return when its context was cancelled")
	}
}

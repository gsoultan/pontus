package listen_test

import (
	"path/filepath"
	"testing"

	"github.com/gsoultan/pontus/pkg/listen"
)

// A zero-downtime upgrade is the only reason the orchestration lock exists, and
// the handover is its second half: the process that stood down has to be able
// to take over once the holder exits. Both log lines promise that
// ("this process takes over when the holder exits"), so this pins the property
// they claim at the layer that has to provide it.
func TestOrchestrationLockIsReacquirableAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration.lock")

	first, err := listen.AcquireOrchestration(path)
	if err != nil {
		t.Fatalf("the first claim failed: %v", err)
	}

	// The second process stands down while the first holds it.
	if _, err := listen.AcquireOrchestration(path); err == nil {
		t.Fatal("two processes both claimed orchestration")
	}

	// The holder exits, as a deploy would have it.
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	// The survivor must now be able to take over. If this fails, a deployment
	// is left with nobody running failover after every upgrade.
	second, err := listen.AcquireOrchestration(path)
	if err != nil {
		t.Fatalf("the lock could not be reacquired after the holder released it: %v", err)
	}
	t.Cleanup(func() { second.Release() })
}

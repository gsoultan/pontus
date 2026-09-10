package listen

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Two failover managers on a five-second tick, each seeing no healthy primary,
// can both promote. That is the split brain the orchestration layer exists to
// avoid, and an upgrade briefly runs two processes — so the lock is what keeps
// the overlap safe.
func TestOrchestrationLockIsExclusive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flock is a unix facility")
	}

	path := filepath.Join(t.TempDir(), "orchestration.lock")

	held, err := AcquireOrchestration(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// A second attempt in this process shares the same open-file table, so the
	// meaningful test is another process — done below. Within one process,
	// re-acquiring a flock on a *different* descriptor is what a second Pontus
	// would do, and that is what this checks.
	second, err := AcquireOrchestration(path)
	if err == nil {
		_ = second.Release()
		// flock is per open file description, so a second Open in the same
		// process is genuinely a second holder attempt.
		t.Error("a second acquire succeeded while the lock was held")
	} else if !errors.Is(err, ErrOrchestrationHeld) {
		t.Errorf("error = %v, want ErrOrchestrationHeld", err)
	}

	// Released, the next process gets it — which is what happens when the old
	// process finishes draining and exits.
	if err := held.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	third, err := AcquireOrchestration(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// The kernel drops the lock when the holder dies, however it dies. A lock
// written as a pid file would survive a kill -9 and lock out every future
// process until someone deleted it by hand.
func TestOrchestrationLockSurvivesAKilledHolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flock is a unix facility")
	}

	path := filepath.Join(t.TempDir(), "orchestration.lock")

	// A child that takes the lock and then blocks.
	holder := exec.Command(os.Args[0], "-test.run=TestOrchestrationLockHolderHelper")
	holder.Env = append(os.Environ(), "PONTUS_LOCK_HELPER="+path)
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatalf("starting the holder: %v", err)
	}

	buf := make([]byte, 16)
	if _, err := stdout.Read(buf); err != nil {
		t.Fatalf("waiting for the holder to take the lock: %v", err)
	}
	if !strings.Contains(string(buf), "held") {
		t.Fatalf("the holder reported %q", buf)
	}

	if _, err := AcquireOrchestration(path); !errors.Is(err, ErrOrchestrationHeld) {
		t.Errorf("acquired a lock another process holds: %v", err)
	}

	// Killed, not asked to stop.
	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("killing the holder: %v", err)
	}
	_ = holder.Wait()

	lock, err := AcquireOrchestration(path)
	if err != nil {
		t.Fatalf("the lock outlived its holder: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestOrchestrationLockHolderHelper is the child of the test above. It is a
// test only so it can be re-executed as this binary.
func TestOrchestrationLockHolderHelper(t *testing.T) {
	path := os.Getenv("PONTUS_LOCK_HELPER")
	if path == "" {
		t.Skip("not the helper child")
	}

	if _, err := AcquireOrchestration(path); err != nil {
		t.Fatalf("helper could not take the lock: %v", err)
	}
	_, _ = os.Stdout.WriteString("held\n")
	select {} // killed by the parent
}

func TestReleasingAnUnheldLockIsHarmless(t *testing.T) {
	var lock *OrchestrationLock
	if err := lock.Release(); err != nil {
		t.Errorf("releasing a nil lock: %v", err)
	}
}

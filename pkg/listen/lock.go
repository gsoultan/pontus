package listen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Orchestration is a singleton per host, and an upgrade briefly runs two
// processes.
//
// SO_REUSEPORT makes the overlap safe for *queries* — two pools opening
// connections to the same database is ordinary. It is not safe for failover: two
// failover managers on a five-second tick, each seeing no healthy primary, can
// both promote. That turns a routine upgrade into the split brain the whole
// orchestration layer exists to avoid.
//
// So the listener may be shared and the orchestrator may not. This is the lock
// that decides which process owns it: an exclusive advisory lock on a file, held
// for the life of the process and released by the kernel when it exits — even if
// it is killed, which a lock written as a PID file in a directory would not
// survive.

// ErrOrchestrationHeld reports that another process owns orchestration.
var ErrOrchestrationHeld = errors.New("another Pontus holds the orchestration lock")

// OrchestrationLock is a held claim on this host's orchestration.
type OrchestrationLock struct {
	file *os.File
}

// AcquireOrchestration takes the lock, or reports who has it.
//
// Non-blocking on purpose: a process starting during an upgrade should serve
// queries immediately and take over orchestration when the old one exits, not
// wait at startup for a lock it may never get.
func AcquireOrchestration(path string) (*OrchestrationLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating the lock directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening the orchestration lock: %w", err)
	}

	if err := lockExclusive(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errWouldBlock) {
			return nil, fmt.Errorf("%w: %s", ErrOrchestrationHeld, path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	// The pid is for a human reading the file, not for the locking — the lock
	// is the kernel's, and a stale pid cannot make it wrong.
	_ = file.Truncate(0)
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())

	return &OrchestrationLock{file: file}, nil
}

// Release gives up the lock so a waiting process can take over.
func (l *OrchestrationLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}

	err := unlock(l.file)
	closeErr := l.file.Close()
	l.file = nil

	return errors.Join(err, closeErr)
}

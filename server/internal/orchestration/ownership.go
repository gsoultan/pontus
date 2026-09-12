package orchestration

import (
	"log/slog"
	"sync"
)

// Orchestration is a singleton per host, and a zero-downtime upgrade briefly
// runs two Pontus processes.
//
// Sharing the listening port is safe for queries — two pools opening
// connections to the same database is ordinary. It is not safe for failover:
// two managers on a five-second tick, each seeing no healthy primary, can both
// promote, which is exactly the split brain this layer exists to prevent.
//
// So a manager only acts while it owns orchestration. Ownership is decided
// outside this package by an advisory lock on a file (`pkg/listen`), because
// the kernel releases that even when a process is killed — a claim written as a
// pid file would outlive its owner and lock out every process after it.
//
// The default is owned. A deployment that has not asked for overlapping
// upgrades has exactly one process, and making it prove that first would mean
// failover silently stopped working for everyone who never turned this on.
var (
	ownershipMu sync.RWMutex
	owner       func() bool
	warnedIdle  sync.Once
)

// SetOwnership installs the predicate that decides whether this process runs
// orchestration. Nil restores the default of always owning it.
func SetOwnership(owned func() bool) {
	ownershipMu.Lock()
	defer ownershipMu.Unlock()
	owner = owned
}

// owned reports whether this process may act on the cluster.
func (m *FailoverManager) owned() bool {
	ownershipMu.RLock()
	check := owner
	ownershipMu.RUnlock()

	if check == nil || check() {
		return true
	}

	warnedIdle.Do(func() {
		slog.Info("Not running orchestration: another Pontus on this host holds it. " +
			"Failover, follow-primary and rejoin are that process's job until it exits.")
	})
	return false
}

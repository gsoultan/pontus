package registry

import (
	"context"
	"log/slog"
	"time"

	"github.com/gsoultan/pontus/pkg/listen"
	orchestration2 "github.com/gsoultan/pontus/server/internal/orchestration"
)

// orchestrationRetryInterval is how often a process that stood down re-tries
// the claim.
//
// Five seconds matches the failover monitor's own tick, so the gap between the
// holder exiting and a manager acting again is bounded by the same interval an
// operator already reasons about. Polling rather than watching the file:
// inotify/kqueue would need a per-platform implementation to learn something
// this cheap to ask for, and an advisory lock has no readiness notification.
const orchestrationRetryInterval = 5 * time.Second

// awaitOrchestration takes over the cluster once the holder releases it.
//
// Runs only when the initial claim failed, and returns as soon as it succeeds —
// a process that holds the lock holds it for its life. Bound to the registry's
// context so shutdown ends it.
func (r *Registry) awaitOrchestration(ctx context.Context, path string) {
	ticker := time.NewTicker(orchestrationRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		lock, err := listen.AcquireOrchestration(path)
		if err != nil {
			continue
		}

		// Written under the lock because StopAll reads it to release, and this
		// goroutine outlives NewRegistry.
		r.mu.Lock()
		r.orchestrationLock = lock
		r.mu.Unlock()

		orchestration2.SetOwnership(func() bool { return true })
		slog.Info("Took over orchestration for this host: the previous holder exited",
			"lock", path)
		return
	}
}

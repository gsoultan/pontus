package health

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"
)

// Target defines the interface for a backend that can be health-checked.
type Target interface {
	Address() string
	SetHealthy(bool)
}

// Checker defines the interface for health checking.
type Checker interface {
	// Start starts the background health checking.
	Start(ctx context.Context)
}

// Monitor is a fast liveness probe: it opens a TCP connection and closes it.
//
// It can only mark a backend **down**, never back up. Reaching the port proves
// the process is listening, not that it can serve a query — a database in
// recovery, out of connections, or refusing auth all accept TCP happily.
// Promotion back to healthy belongs to pool.Server.deepCheck, which runs an
// actual query and re-detects the primary/replica role.
//
// Both used to write SetHealthy, at different depths and cadences, so this
// probe could flip a node back to healthy in the gap between deep checks
// purely because its socket was open.
type Monitor struct {
	mu       sync.RWMutex
	backends []Target
	interval time.Duration
	timeout  time.Duration
}

// NewMonitor creates a new Monitor.
func NewMonitor(nodes []Target, interval, timeout time.Duration) *Monitor {
	return &Monitor{
		backends: nodes,
		interval: interval,
		timeout:  timeout,
	}
}

// UpdateNodes updates the list of backends to monitor.
func (m *Monitor) UpdateNodes(nodes []Target) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backends = slices.Clone(nodes)
}

// Start runs the health checks periodically.
func (m *Monitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkAll(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (m *Monitor) checkAll(ctx context.Context) {
	m.mu.RLock()
	backends := slices.Clone(m.backends)
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for b := range slices.Values(backends) {
		wg.Go(func() {
			m.checkOne(ctx, b)
		})
	}
	wg.Wait()
}

func (m *Monitor) checkOne(ctx context.Context, b Target) {
	checkCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	d := net.Dialer{}
	conn, err := d.DialContext(checkCtx, "tcp", b.Address())
	if err != nil {
		// Unreachable is unambiguous, and catching it on this interval is the
		// point of a shallow probe: it fails a node far sooner than the deep
		// check would.
		b.SetHealthy(false)
		return
	}

	// Reachable proves nothing beyond "something is listening", so the node is
	// left as it is and deepCheck decides whether it can serve traffic.
	conn.Close()
}

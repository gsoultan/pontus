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

// Monitor implements the Checker interface.
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

	b.SetHealthy(err == nil)

	if err == nil {
		conn.Close()
	}
}

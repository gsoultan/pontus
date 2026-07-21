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
	IsHealthy() bool
	SetHealthy(bool)
}

// Monitor implements health checking with observer pattern.
type Monitor struct {
	mu        sync.RWMutex
	targets   []Target
	listeners []Listener
	interval  time.Duration
	timeout   time.Duration
}

// NewMonitor creates a new health monitor.
func NewMonitor(interval, timeout time.Duration) *Monitor {
	m := new(Monitor)
	m.interval = interval
	m.timeout = timeout
	return m
}

// Register adds a target to be monitored.
func (m *Monitor) Register(t Target) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets = append(m.targets, t)
}

// Subscribe adds a listener for state changes.
func (m *Monitor) Subscribe(l Listener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listeners = append(m.listeners, l)
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
	targets := slices.Clone(m.targets)
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for t := range slices.Values(targets) {
		wg.Go(func() {
			m.checkOne(ctx, t)
		})
	}
	wg.Wait()
}

func (m *Monitor) checkOne(ctx context.Context, t Target) {
	checkCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	d := net.Dialer{}
	conn, err := d.DialContext(checkCtx, "tcp", t.Address())

	healthy := err == nil
	if healthy {
		conn.Close()
	}

	previous := t.IsHealthy()
	t.SetHealthy(healthy)

	if healthy != previous {
		m.notify(StateChange{
			Address: t.Address(),
			Healthy: healthy,
		})
	}
}

func (m *Monitor) notify(change StateChange) {
	m.mu.RLock()
	listeners := slices.Clone(m.listeners)
	m.mu.RUnlock()

	for l := range slices.Values(listeners) {
		l.OnStateChange(change)
	}
}

package system

import (
	"runtime"
	"sync/atomic"
	"time"
)

// ResourceStats holds system resource usage information.
type ResourceStats struct {
	CPUUsage      float64
	MemoryUsage   uint64 // In bytes
	NumGoroutines int
	NumFDs        int
}

// Monitor tracks system resources periodically.
type Monitor struct {
	cpuUsage    atomic.Uint64 // stored as math.Float64bits
	memUsage    atomic.Uint64
	goroutines  atomic.Int32
	fds         atomic.Int32
	lastCPUTime int64
	lastCheck   time.Time
	stop        chan struct{}
}

// NewMonitor creates a new resource monitor.
func NewMonitor() *Monitor {
	return &Monitor{
		stop: make(chan struct{}),
	}
}

// Start begins periodic resource tracking.
func (m *Monitor) Start(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.update()
			case <-m.stop:
				return
			}
		}
	}()
}

// Stop stops the periodic resource tracking.
func (m *Monitor) Stop() {
	close(m.stop)
}

func (m *Monitor) update() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.memUsage.Store(ms.Alloc)
	m.goroutines.Store(int32(runtime.NumGoroutine()))

	// FD count is OS specific, skipping for generic implementation but would use
	// /proc/self/fd on Linux or GetProcessInformation on Windows.
}

// Stats returns the latest resource statistics.
func (m *Monitor) Stats() ResourceStats {
	return ResourceStats{
		MemoryUsage:   m.memUsage.Load(),
		NumGoroutines: int(m.goroutines.Load()),
		NumFDs:        int(m.fds.Load()),
	}
}

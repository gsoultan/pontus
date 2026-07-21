package pool

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/gsoultan/pontus/internal/system"
)

type AdaptivePoolController struct {
	pool            *Server
	monitor         *system.Monitor
	targetLatency   time.Duration
	targetErrorRate float64
	minLatency      time.Duration
	maxRPS          float64
	lastRequests    int64
	lastCheck       time.Time

	// Status tracking
	isThrottled    bool
	throttleReason string
	suggestions    []Suggestion
	mu             sync.RWMutex
}

type Suggestion struct {
	Title       string
	Description string
	Level       string
	Action      string
}

func NewAdaptivePoolController(p *Server, m *system.Monitor) *AdaptivePoolController {
	return &AdaptivePoolController{
		pool:            p,
		monitor:         m,
		targetLatency:   50 * time.Millisecond,
		targetErrorRate: 0.01,
		minLatency:      math.MaxInt64,
		suggestions:     make([]Suggestion, 0),
	}
}

func (c *AdaptivePoolController) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.adjust()
		}
	}
}

func (c *AdaptivePoolController) Status() (isThrottled bool, reason string, suggestions []Suggestion) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Copy suggestions to avoid race conditions when the caller uses them
	copiedSuggestions := make([]Suggestion, len(c.suggestions))
	copy(copiedSuggestions, c.suggestions)

	return c.isThrottled, c.throttleReason, copiedSuggestions
}
func (c *AdaptivePoolController) adjust() {
	now := time.Now()
	latency := c.pool.Latency()
	totalRequests := c.pool.Stats().TotalRequests

	if c.lastCheck.IsZero() {
		c.lastCheck = now
		c.lastRequests = totalRequests
		return
	}

	interval := now.Sub(c.lastCheck)
	rps := float64(totalRequests-c.lastRequests) / interval.Seconds()

	// 1. Probe Min Latency
	if latency > 0 && latency < c.minLatency {
		c.minLatency = latency
	}

	// 2. Probe Max Bandwidth (RPS)
	if rps > c.maxRPS {
		c.maxRPS = rps
	}

	// 3. BBR-style pooling: Target size = Max RPS * Min Latency (BDP)
	// We add a gain factor to allow for some growth and handle bursts
	gain := 1.25

	// 4. Resource-Aware Throttling
	throttled := false
	reason := ""
	newSuggestions := make([]Suggestion, 0)

	if c.monitor != nil {
		stats := c.monitor.Stats()
		// If memory is high (e.g., > 2GB for Pontus overhead), reduce gain
		if stats.MemoryUsage > 2*1024*1024*1024 {
			gain = 1.0
			throttled = true
			reason = "High memory usage"
			slog.Warn("High memory usage detected, reducing pool growth gain", "mem", stats.MemoryUsage)
		}
		// If goroutines are extremely high (e.g. > 150k), start throttling
		if stats.NumGoroutines > 150000 {
			gain = 0.8
			throttled = true
			reason = "Excessive goroutine count"
			slog.Warn("Extremely high goroutine count, throttling pool", "goroutines", stats.NumGoroutines)
		}
	}

	targetPoolSize := int32(math.Ceil(c.maxRPS * c.minLatency.Seconds() * gain))

	// Performance Advisor Logic
	if c.monitor != nil {
		stats := c.monitor.Stats()
		if stats.CPUUsage > 80.0 {
			newSuggestions = append(newSuggestions, Suggestion{
				Title:       "High CPU Usage",
				Description: "System CPU usage is above 80%. This can lead to increased latency and decreased throughput.",
				Level:       "warning",
				Action:      "Increase CPU resources or reduce transaction complexity.",
			})
			slog.Warn("Performance Advisor: High CPU usage. Consider increasing CPU resources or reducing transaction complexity.")
		}
		if stats.MemoryUsage > 4*1024*1024*1024 {
			newSuggestions = append(newSuggestions, Suggestion{
				Title:       "High Memory Usage",
				Description: "System memory usage is high. This may lead to OOM and performance degradation.",
				Level:       "critical",
				Action:      "Tune ulimit and buffer sizes, or increase system memory.",
			})
			slog.Warn("Performance Advisor: High Memory usage. Consider tuning ulimit and buffer sizes.")
		}
		if stats.NumGoroutines > 200000 {
			newSuggestions = append(newSuggestions, Suggestion{
				Title:       "High Concurrency",
				Description: "Extremely high goroutine count detected.",
				Level:       "warning",
				Action:      "Ensure OS network stack is tuned for high connection count.",
			})
			slog.Warn("Performance Advisor: Extremely high concurrency. Ensure OS network stack is tuned for high connection count.")
		}
		if c.minLatency > 100*time.Millisecond {
			newSuggestions = append(newSuggestions, Suggestion{
				Title:       "High DB Latency",
				Description: "Database minimum latency is above 100ms.",
				Level:       "info",
				Action:      "Consider tuning database indexes or adding replicas.",
			})
			slog.Info("Performance Advisor: High database latency detected. Consider tuning database indexes or adding replicas.")
		}
	}

	c.mu.Lock()
	c.isThrottled = throttled
	c.throttleReason = reason
	c.suggestions = newSuggestions
	c.mu.Unlock()

	// Ensure some minimums and respect configured maximums
	if targetPoolSize < 10 {
		targetPoolSize = 10
	}
	if targetPoolSize > c.pool.maxConns {
		targetPoolSize = c.pool.maxConns
	}

	currentMax := c.pool.currentMax.Load()
	if targetPoolSize != currentMax {
		slog.Info("BBR-style adaptive pooling adjustment",
			"address", c.pool.Address(),
			"old", currentMax,
			"new", targetPoolSize,
			"rps", rps,
			"min_latency", c.minLatency)
		c.pool.currentMax.Store(targetPoolSize)
	}

	c.lastCheck = now
	c.lastRequests = totalRequests
}

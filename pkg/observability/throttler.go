package observability

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ThrottlingPolicy defines the thresholds for throttling a query fingerprint.
type ThrottlingPolicy struct {
	MaxLatency     time.Duration
	MaxErrorRate   float64 // 0.0 to 1.0
	Limit          rate.Limit
	Burst          int
	MinSampleCount int
}

// DefaultThrottlingPolicy provides sensible defaults.
var DefaultThrottlingPolicy = ThrottlingPolicy{
	MaxLatency:     2 * time.Second,
	MaxErrorRate:   0.1, // 10%
	Limit:          rate.Limit(5),
	Burst:          10,
	MinSampleCount: 100,
}

// DynamicThrottler manages rate limiters for query fingerprints based on observed behavior.
type DynamicThrottler struct {
	mu       sync.RWMutex
	limiters map[string]*rate.Limiter
	policy   ThrottlingPolicy
}

// NewDynamicThrottler creates a new DynamicThrottler.
func NewDynamicThrottler(policy ThrottlingPolicy) *DynamicThrottler {
	return &DynamicThrottler{
		limiters: make(map[string]*rate.Limiter),
		policy:   policy,
	}
}

// Allow checks if a query fingerprint is allowed to proceed.
func (dt *DynamicThrottler) Allow(fingerprint string) bool {
	dt.mu.RLock()
	limiter, ok := dt.limiters[fingerprint]
	dt.mu.RUnlock()

	if !ok {
		return true
	}

	return limiter.Allow()
}

// IsThrottled checks if a query fingerprint is currently being throttled without consuming tokens.
func (dt *DynamicThrottler) IsThrottled(fingerprint string) bool {
	dt.mu.RLock()
	_, ok := dt.limiters[fingerprint]
	dt.mu.RUnlock()
	return ok
}

// Update evaluates query stats and updates the throttling state for a fingerprint.
func (dt *DynamicThrottler) Update(fingerprint string, stat QueryStat) {
	if stat.Count < int64(dt.policy.MinSampleCount) {
		return
	}

	avgLatency := time.Duration(0)
	if stat.Count > 0 {
		avgLatency = stat.TotalTime / time.Duration(stat.Count)
	}
	errorRate := 0.0
	if stat.Count > 0 {
		errorRate = float64(stat.ErrorCount) / float64(stat.Count)
	}

	shouldThrottle := avgLatency > dt.policy.MaxLatency || errorRate > dt.policy.MaxErrorRate

	dt.mu.Lock()
	defer dt.mu.Unlock()

	if shouldThrottle {
		if _, ok := dt.limiters[fingerprint]; !ok {
			dt.limiters[fingerprint] = rate.NewLimiter(dt.policy.Limit, dt.policy.Burst)
		}
	} else {
		delete(dt.limiters, fingerprint)
	}
}

// ThrottledQueries returns a list of fingerprints currently being throttled.
func (dt *DynamicThrottler) ThrottledQueries() []string {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	res := make([]string, 0, len(dt.limiters))
	for fp := range dt.limiters {
		res = append(res, fp)
	}
	return res
}

var DefaultThrottler = NewDynamicThrottler(DefaultThrottlingPolicy)

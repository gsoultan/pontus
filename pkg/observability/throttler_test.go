package observability

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestDynamicThrottler(t *testing.T) {
	policy := ThrottlingPolicy{
		MaxLatency:     100 * time.Millisecond,
		MaxErrorRate:   0.1,
		Limit:          rate.Limit(1),
		Burst:          1,
		MinSampleCount: 10,
	}

	dt := NewDynamicThrottler(policy)
	fingerprint := "SELECT * FROM users"

	// Initial state: not throttled
	if !dt.Allow(fingerprint) {
		t.Error("Should allow initially")
	}

	// Update with good stats
	dt.Update(fingerprint, QueryStat{
		Query:      fingerprint,
		Count:      100,
		TotalTime:  5000 * time.Millisecond, // 50ms avg
		ErrorCount: 2,                       // 2% error rate
	})

	if !dt.Allow(fingerprint) {
		t.Error("Should still allow after good stats")
	}

	// Update with bad latency
	dt.Update(fingerprint, QueryStat{
		Query:      fingerprint,
		Count:      100,
		TotalTime:  20000 * time.Millisecond, // 200ms avg (> 100ms)
		ErrorCount: 2,
	})

	if !dt.IsThrottled(fingerprint) {
		t.Error("Should be throttled after bad latency")
	}

	if !dt.Allow(fingerprint) {
		// First one allowed due to burst
	}
	if dt.Allow(fingerprint) {
		t.Error("Should NOT allow second time after bad latency")
	}

	if !dt.IsThrottled(fingerprint) {
		t.Error("IsThrottled should return true")
	}

	// Update with bad error rate but good latency
	dt.Update(fingerprint, QueryStat{
		Query:      fingerprint,
		Count:      100,
		TotalTime:  5000 * time.Millisecond,
		ErrorCount: 20, // 20% (> 10%)
	})

	if !dt.IsThrottled(fingerprint) {
		t.Error("Should be throttled after bad error rate")
	}

	// Recover
	dt.Update(fingerprint, QueryStat{
		Query:      fingerprint,
		Count:      100,
		TotalTime:  5000 * time.Millisecond,
		ErrorCount: 0,
	})

	if !dt.Allow(fingerprint) {
		t.Error("Should allow after recovery")
	}
}

func TestSyncThrottler(t *testing.T) {
	dt := NewDynamicThrottler(DefaultThrottlingPolicy)
	tracker := NewQueryTracker(10)

	fingerprint := "BAD QUERY"
	tracker.Record(fingerprint, 5*time.Second, nil) // > 2s default

	// Need enough samples
	for i := 0; i < 100; i++ {
		tracker.Record(fingerprint, 5*time.Second, nil)
	}

	tracker.SyncThrottler(dt)

	if !dt.IsThrottled(fingerprint) {
		t.Error("Should be throttled after sync")
	}
}

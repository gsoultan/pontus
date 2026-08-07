package observability

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/pkg/observability/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeMetricStore records what the tracker writes and replays what it is
// seeded with, so hydration can be tested without touching disk.
type fakeMetricStore struct {
	history  []*domain.MetricSnapshot
	top      []*domain.TopQuery
	requests int64
	errors   int64
	saved    []*domain.MetricSnapshot
}

func (f *fakeMetricStore) SaveSnapshot(_ context.Context, snap *domain.MetricSnapshot) error {
	f.saved = append(f.saved, snap)
	return nil
}

func (f *fakeMetricStore) GetHistory(_ context.Context, _, _ time.Time) ([]*domain.MetricSnapshot, error) {
	return f.history, nil
}

func (f *fakeMetricStore) SaveTopQueries(_ context.Context, stats []*domain.TopQuery) error {
	f.top = stats
	return nil
}

func (f *fakeMetricStore) GetTopQueries(_ context.Context, _, _ time.Time, _ int) ([]*domain.TopQuery, error) {
	return f.top, nil
}

func (f *fakeMetricStore) SaveCounters(_ context.Context, requests, errors int64) error {
	f.requests, f.errors = requests, errors
	return nil
}

func (f *fakeMetricStore) LoadCounters(_ context.Context) (int64, int64, error) {
	return f.requests, f.errors, nil
}

func (f *fakeMetricStore) Prune(_ context.Context, _ time.Time) (int64, error) { return 0, nil }
func (f *fakeMetricStore) Close() error                                        { return nil }

var _ store.MetricStore = (*fakeMetricStore)(nil)

// A restart used to leave the trend chart blank for a full hour and reset the
// lifetime counters to zero, even though the data was already on disk.
func TestSetStoreHydratesHistoryAndCounters(t *testing.T) {
	base := time.Now().Add(-30 * time.Minute)
	fake := &fakeMetricStore{
		requests: 12_345,
		errors:   67,
	}
	for i := range 10 {
		fake.history = append(fake.history, &domain.MetricSnapshot{
			Timestamp:         timestamppb.New(base.Add(time.Duration(i) * time.Minute)),
			RequestsPerSecond: float32(i + 1),
			ErrorRate:         0.01,
			LatencyMs:         5,
		})
	}

	tracker := NewQueryTracker(1000)
	if got := len(slicesFrom(tracker.GetHistory())); got != 0 {
		t.Fatalf("fresh tracker has %d snapshots, want 0", got)
	}

	tracker.SetStore(fake)

	restored := slicesFrom(tracker.GetHistory())
	if len(restored) != 10 {
		t.Fatalf("hydrated %d snapshots, want 10", len(restored))
	}
	if restored[0].RPS != 1 || restored[9].RPS != 10 {
		t.Errorf("hydrated RPS series = %v..%v, want 1..10", restored[0].RPS, restored[9].RPS)
	}

	total, errors, _ := tracker.GlobalStats()
	if total != 12_345 || errors != 67 {
		t.Errorf("counters = (%d, %d), want (12345, 67)", total, errors)
	}
}

// RPS was totalRequests/uptime — a lifetime average that barely moves for a
// burst late in a long run. It must report the rate over the last window.
func TestGlobalStatsReportsWindowedRate(t *testing.T) {
	tracker := NewQueryTracker(1000)

	// Simulate a long quiet period: many requests spread over a long uptime.
	tracker.startTime = time.Now().Add(-10 * time.Hour)
	for range 100 {
		tracker.Record("SELECT 1", time.Millisecond, nil)
	}

	// Prime the window, then burst.
	tracker.liveCursor.delta(tracker.totalRequests.Load(), tracker.totalErrors.Load(),
		time.Now().Add(-time.Second))
	for range 500 {
		tracker.Record("SELECT 2", time.Millisecond, nil)
	}
	tracker.sampleRate()

	_, _, rps := tracker.GlobalStats()

	// Lifetime average would be 600/36000 ≈ 0.017. The windowed rate must
	// reflect the burst instead.
	if rps < 100 {
		t.Fatalf("windowed RPS = %.2f, want the burst rate (lifetime average would be ~0.02)", rps)
	}
}

func TestRateCursorIgnoresCounterReset(t *testing.T) {
	var c rateCursor
	now := time.Now()

	c.delta(1000, 10, now)
	// Counters restored to a smaller value (or reset) must not yield a
	// negative rate.
	rps, _, ok := c.delta(5, 0, now.Add(time.Second))
	if ok {
		t.Fatalf("expected the reset sample to be rejected, got rps=%.2f", rps)
	}
}

// Eviction used to clear() the whole shard, so Top Queries reset to empty
// under high query diversity. Hot statements must survive.
func TestEvictStaleKeepsRecentEntries(t *testing.T) {
	now := time.Now()
	stats := map[string]*QueryStat{}
	for i := range 10 {
		key := string(rune('a' + i))
		stats[key] = &QueryStat{
			Query:    key,
			Count:    int64(i),
			LastSeen: now.Add(time.Duration(i) * time.Minute),
		}
	}

	evictStale(stats)

	if len(stats) == 0 {
		t.Fatal("evictStale wiped the shard; it must retain the most recent half")
	}
	if len(stats) >= 10 {
		t.Fatalf("evictStale kept %d of 10 entries; the map must shrink", len(stats))
	}
	// The newest entry must always survive.
	if _, ok := stats["j"]; !ok {
		t.Error("evictStale dropped the most recently seen entry")
	}
}

func TestEvictStaleBoundsIdenticalTimestamps(t *testing.T) {
	now := time.Now()
	stats := map[string]*QueryStat{}
	for i := range 8 {
		key := string(rune('a' + i))
		stats[key] = &QueryStat{Query: key, LastSeen: now}
	}

	evictStale(stats)

	if len(stats) >= 8 {
		t.Fatalf("kept %d of 8 entries with identical timestamps; map must still shrink", len(stats))
	}
}

func slicesFrom(seq func(func(MetricSnapshot) bool)) []MetricSnapshot {
	var out []MetricSnapshot
	seq(func(s MetricSnapshot) bool {
		out = append(out, s)
		return true
	})
	return out
}

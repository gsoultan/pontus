package observability

import (
	"context"
	"iter"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/pkg/observability/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type MetricSnapshot struct {
	Timestamp time.Time `json:"ts,omitzero"`
	RPS       float64   `json:"rps,omitzero"`
	ErrorRate float64   `json:"err_rate,omitzero"`
	Latency   float64   `json:"lat,omitzero"`
}

type QueryStat struct {
	Query      string
	Count      int64
	TotalTime  time.Duration
	MaxTime    time.Duration
	LastSeen   time.Time
	ErrorCount int64
}

const numShards = 16

type shard struct {
	mu    sync.RWMutex
	stats map[string]*QueryStat
}

// historyWindow is how many one-minute snapshots the in-memory ring holds.
const historyWindow = 60

type rateCursor struct {
	mu     sync.Mutex
	total  int64
	errors int64
	at     time.Time
}

// delta returns the per-second rate and error ratio since the previous call,
// advancing the cursor. ok is false on the first call, when there is no prior
// sample to difference against.
func (c *rateCursor) delta(total, errors int64, now time.Time) (rps, errRate float64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.at.IsZero() {
		c.total, c.errors, c.at = total, errors, now
		return 0, 0, false
	}

	elapsed := now.Sub(c.at).Seconds()
	dTotal := total - c.total
	dErrors := errors - c.errors
	c.total, c.errors, c.at = total, errors, now

	if elapsed <= 0 {
		return 0, 0, false
	}
	// Counters only move forward; a negative delta means they were restored or
	// reset underneath us, so skip the sample rather than emit a negative rate.
	if dTotal < 0 || dErrors < 0 {
		return 0, 0, false
	}
	if dTotal > 0 {
		errRate = float64(dErrors) / float64(dTotal)
	}
	return float64(dTotal) / elapsed, errRate, true
}

type QueryTracker struct {
	shards        [numShards]shard
	history       []MetricSnapshot
	maxSize       int
	historyMu     sync.RWMutex
	startTime     time.Time
	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	store         store.MetricStore

	// Live rate, resampled on a short interval. RPS used to be reported as
	// totalRequests/uptime — a lifetime average that flattens out and barely
	// responds to a spike hours into a run. These hold a windowed rate instead.
	currentRPS     atomic.Uint64 // math.Float64bits
	currentErrRate atomic.Uint64 // math.Float64bits
	hasRate        atomic.Bool

	liveCursor rateCursor // drives currentRPS, sampled every few seconds
	snapCursor rateCursor // drives persisted snapshots, sampled every minute
}

var DefaultTracker = NewQueryTracker(1000)

func NewQueryTracker(maxSize int) *QueryTracker {
	t := &QueryTracker{
		maxSize:   maxSize,
		startTime: time.Now(),
	}
	for i := range numShards {
		t.shards[i].stats = make(map[string]*QueryStat)
	}
	return t
}

// evictStale drops the least-recently-seen half of a full shard.
//
// This used to be clear(), which wiped every statistic in the shard the moment
// it filled — so Top Queries would abruptly reset to nothing under high query
// diversity, exactly when the data was most interesting. Halving keeps the hot
// statements and bounds the map with amortised O(n) work.
func evictStale(stats map[string]*QueryStat) {
	if len(stats) == 0 {
		return
	}

	seen := make([]time.Time, 0, len(stats))
	for _, s := range stats {
		seen = append(seen, s.LastSeen)
	}
	slices.SortFunc(seen, func(a, b time.Time) int { return a.Compare(b) })

	cutoff := seen[len(seen)/2]
	for query, s := range stats {
		if !s.LastSeen.After(cutoff) {
			delete(stats, query)
		}
	}

	// A shard whose entries all share a timestamp survives the cutoff intact;
	// fall back to clearing so the map still shrinks and stays bounded.
	if len(stats) >= len(seen) {
		clear(stats)
	}
}

func (t *QueryTracker) getShard(query string) *shard {
	h := uint32(2166136261)
	for i := range len(query) {
		h *= 16777619
		h ^= uint32(query[i])
	}
	return &t.shards[h%numShards]
}

func (t *QueryTracker) Record(query string, duration time.Duration, err error) {
	t.totalRequests.Add(1)
	if err != nil {
		t.totalErrors.Add(1)
	}

	if query == "" {
		return
	}

	srd := t.getShard(query)
	srd.mu.Lock()
	defer srd.mu.Unlock()

	s, ok := srd.stats[query]
	if !ok {
		if len(srd.stats) >= t.maxSize/numShards {
			evictStale(srd.stats)
		}
		s = &QueryStat{Query: query}
		srd.stats[query] = s
	}

	s.Count++
	s.TotalTime += duration
	if duration > s.MaxTime {
		s.MaxTime = duration
	}
	if err != nil {
		s.ErrorCount++
	}
	s.LastSeen = time.Now()
}

func (t *QueryTracker) Uptime() time.Duration {
	return time.Since(t.startTime)
}

// GlobalStats returns lifetime totals and the current windowed request rate.
//
// The rate is the throughput over the last sampling interval, not the average
// since boot. Until the first interval completes it falls back to the lifetime
// average, which is the best estimate available for a freshly started process.
func (t *QueryTracker) GlobalStats() (int64, int64, float64) {
	total := t.totalRequests.Load()
	errors := t.totalErrors.Load()

	if t.hasRate.Load() {
		return total, errors, math.Float64frombits(t.currentRPS.Load())
	}

	uptime := t.Uptime().Seconds()
	rps := 0.0
	if uptime > 0 {
		rps = float64(total) / uptime
	}
	return total, errors, rps
}

// ErrorRate returns the windowed error ratio, falling back to the lifetime
// ratio before the first sample.
func (t *QueryTracker) ErrorRate() float64 {
	if t.hasRate.Load() {
		return math.Float64frombits(t.currentErrRate.Load())
	}
	total := t.totalRequests.Load()
	if total == 0 {
		return 0
	}
	return float64(t.totalErrors.Load()) / float64(total)
}

// sampleRate refreshes the live windowed rate from the cumulative counters.
func (t *QueryTracker) sampleRate() {
	rps, errRate, ok := t.liveCursor.delta(
		t.totalRequests.Load(), t.totalErrors.Load(), time.Now())
	if !ok {
		return
	}
	t.currentRPS.Store(math.Float64bits(rps))
	t.currentErrRate.Store(math.Float64bits(errRate))
	t.hasRate.Store(true)
}

// StartRateSampler keeps the live rate fresh. The interval bounds how quickly
// the dashboard's RPS tile reacts, so it should stay well under the poll rate.
func (t *QueryTracker) StartRateSampler(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		t.sampleRate() // prime the cursor so the first tick yields a real delta
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.sampleRate()
			}
		}
	}()
}

func (t *QueryTracker) GetTop(limit int) []QueryStat {
	all := make([]QueryStat, 0, t.maxSize)
	for i := range numShards {
		t.shards[i].mu.RLock()
		for _, s := range t.shards[i].stats {
			all = append(all, *s)
		}
		t.shards[i].mu.RUnlock()
	}

	slices.SortFunc(all, func(a, b QueryStat) int {
		if a.Count > b.Count {
			return -1
		}
		if a.Count < b.Count {
			return 1
		}
		return 0
	})

	if len(all) > limit {
		return all[:limit]
	}
	return all
}

func (t *QueryTracker) SetStore(s store.MetricStore) {
	t.historyMu.Lock()
	t.store = s
	t.historyMu.Unlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		t.hydrate(ctx, s)
	}
}

// hydrate restores state that outlives the process from the metric store.
//
// Without this the trend chart is blank for a full hour after every restart
// and the lifetime counters read zero — while the data sits in metrics.db the
// whole time. The in-memory ring is a cache over the store, not a second
// source of truth.
//
// Deliberately *not* hydrated: the per-query shard stats. Those are live
// working state for "queries seen by this process"; seeding them from
// historical aggregates would double-count as new traffic arrives. The
// dashboard's historical top-queries view already reads the store directly.
func (t *QueryTracker) hydrate(ctx context.Context, s store.MetricStore) {
	now := time.Now()

	if history, err := s.GetHistory(ctx, now.Add(-historyWindow*time.Minute), now); err == nil && len(history) > 0 {
		restored := make([]MetricSnapshot, 0, len(history))
		for _, snap := range history {
			if snap == nil || snap.Timestamp == nil {
				continue
			}
			restored = append(restored, MetricSnapshot{
				Timestamp: snap.Timestamp.AsTime(),
				RPS:       float64(snap.RequestsPerSecond),
				ErrorRate: float64(snap.ErrorRate),
				Latency:   float64(snap.LatencyMs),
			})
		}
		if len(restored) > historyWindow {
			restored = restored[len(restored)-historyWindow:]
		}
		t.historyMu.Lock()
		t.history = restored
		t.historyMu.Unlock()
	}

	if total, errors, err := s.LoadCounters(ctx); err == nil && total > 0 {
		t.totalRequests.Store(total)
		t.totalErrors.Store(errors)
		// Prime both cursors at the restored values so the first delta measures
		// traffic since restart rather than reporting the entire restored
		// total as one interval's worth of requests.
		t.liveCursor.delta(total, errors, now)
		t.snapCursor.delta(total, errors, now)
	}
}

// Flush persists the current counters and one final snapshot. Called on
// graceful shutdown so the last interval before a restart is not lost.
func (t *QueryTracker) Flush(ctx context.Context) {
	s := t.GetStore()
	if s == nil {
		return
	}
	t.snapshot()
	_ = s.SaveCounters(ctx, t.totalRequests.Load(), t.totalErrors.Load())
}

func (t *QueryTracker) GetStore() store.MetricStore {
	t.historyMu.RLock()
	defer t.historyMu.RUnlock()
	return t.store
}

func (t *QueryTracker) StartHistoryCollector(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.snapshot()
			}
		}
	}()
}

func (t *QueryTracker) snapshot() {
	// Each persisted row is the rate over the interval that just elapsed, not
	// the average since boot. A chart of lifetime averages converges to a flat
	// line and hides exactly the spikes it exists to show.
	rps, errRate, ok := t.snapCursor.delta(
		t.totalRequests.Load(), t.totalErrors.Load(), time.Now())
	if !ok {
		return // first tick only primes the cursor
	}

	var avgLat float64
	var count int64

	for i := range numShards {
		t.shards[i].mu.RLock()
		for _, s := range t.shards[i].stats {
			if s.Count > 0 {
				avgLat += float64(s.TotalTime.Milliseconds()) / float64(s.Count)
				count++
			}
		}
		t.shards[i].mu.RUnlock()
	}

	if count > 0 {
		avgLat /= float64(count)
	}

	snap := MetricSnapshot{
		Timestamp: time.Now(),
		RPS:       rps,
		ErrorRate: errRate,
		Latency:   avgLat,
	}

	t.historyMu.Lock()
	t.history = append(t.history, snap)
	if len(t.history) > historyWindow {
		t.history = t.history[len(t.history)-historyWindow:]
	}
	s := t.store
	t.historyMu.Unlock()

	if s != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = s.SaveSnapshot(ctx, &domain.MetricSnapshot{
			Timestamp:         timestamppb.New(snap.Timestamp),
			RequestsPerSecond: float32(snap.RPS),
			ErrorRate:         float32(snap.ErrorRate),
			LatencyMs:         float32(snap.Latency),
		})

		// Checkpoint the lifetime counters alongside each snapshot so an
		// ungraceful exit loses at most one interval rather than everything.
		_ = s.SaveCounters(ctx, t.totalRequests.Load(), t.totalErrors.Load())

		// Occasionally save top queries too (e.g. every 5 minutes or every snapshot)
		// For now, let's do it every snapshot to have granular history.
		top := t.GetTop(20)
		protoTop := make([]*domain.TopQuery, len(top))
		for i, q := range top {
			avg := int64(0)
			if q.Count > 0 {
				avg = q.TotalTime.Milliseconds() / q.Count
			}
			protoTop[i] = &domain.TopQuery{
				Query:       q.Query,
				Count:       q.Count,
				TotalTimeMs: q.TotalTime.Milliseconds(),
				MaxTimeMs:   q.MaxTime.Milliseconds(),
				AvgTimeMs:   avg,
				LastSeen:    timestamppb.New(q.LastSeen),
				ErrorCount:  q.ErrorCount,
			}
		}
		_ = s.SaveTopQueries(ctx, protoTop)
	}
}

// GetHistory uses Go 1.26 iterators for efficient data streaming
func (t *QueryTracker) GetHistory() iter.Seq[MetricSnapshot] {
	return func(yield func(MetricSnapshot) bool) {
		t.historyMu.RLock()
		defer t.historyMu.RUnlock()
		for _, s := range t.history {
			if !yield(s) {
				return
			}
		}
	}
}

// SyncThrottler updates the given DynamicThrottler with the current query statistics.
func (t *QueryTracker) SyncThrottler(dt *DynamicThrottler) {
	for i := range numShards {
		t.shards[i].mu.RLock()
		for _, s := range t.shards[i].stats {
			dt.Update(s.Query, *s)
		}
		t.shards[i].mu.RUnlock()
	}
}

// StartBackgroundSync starts a goroutine that periodically syncs the tracker with the throttler.
func StartBackgroundSync(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				DefaultTracker.SyncThrottler(DefaultThrottler)
			}
		}
	}()
}

// StartPruner starts a background goroutine to prune old metrics.
func (t *QueryTracker) StartPruner(ctx context.Context, interval time.Duration, retention time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.historyMu.RLock()
				s := t.store
				t.historyMu.RUnlock()

				if s != nil {
					olderThan := time.Now().Add(-retention)
					_, _ = s.Prune(ctx, olderThan)
				}
			}
		}
	}()
}

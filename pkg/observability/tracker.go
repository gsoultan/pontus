package observability

import (
	"context"
	"iter"
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

type FirewallStats struct {
	BlockedByWord    atomic.Int64
	BlockedByPattern atomic.Int64
	BlockedBySize    atomic.Int64
}

const numShards = 16

type shard struct {
	mu    sync.RWMutex
	stats map[string]*QueryStat
}

type QueryTracker struct {
	shards        [numShards]shard
	history       []MetricSnapshot
	maxSize       int
	historyMu     sync.RWMutex
	startTime     time.Time
	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	firewallStats FirewallStats
	store         store.MetricStore
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
			clear(srd.stats)
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

func (t *QueryTracker) RecordFirewallViolation(reason string) {
	switch reason {
	case "word":
		t.firewallStats.BlockedByWord.Add(1)
	case "pattern":
		t.firewallStats.BlockedByPattern.Add(1)
	case "size":
		t.firewallStats.BlockedBySize.Add(1)
	}
}

func (t *QueryTracker) GetFirewallStats() (int64, int64, int64) {
	return t.firewallStats.BlockedByWord.Load(),
		t.firewallStats.BlockedByPattern.Load(),
		t.firewallStats.BlockedBySize.Load()
}

func (t *QueryTracker) Uptime() time.Duration {
	return time.Since(t.startTime)
}

func (t *QueryTracker) GlobalStats() (int64, int64, float64) {
	uptime := t.Uptime().Seconds()
	total := t.totalRequests.Load()
	errors := t.totalErrors.Load()
	rps := 0.0
	if uptime > 0 {
		rps = float64(total) / uptime
	}
	return total, errors, rps
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
	_, errors, rps := t.GlobalStats()

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

	total := t.totalRequests.Load()
	errRate := 0.0
	if total > 0 {
		errRate = float64(errors) / float64(total)
	}

	snap := MetricSnapshot{
		Timestamp: time.Now(),
		RPS:       rps,
		ErrorRate: errRate,
		Latency:   avgLat,
	}

	t.historyMu.Lock()
	t.history = append(t.history, snap)
	if len(t.history) > 60 { // Keep last hour in memory
		t.history = t.history[1:]
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

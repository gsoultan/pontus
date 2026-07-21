package observability

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/pkg/observability/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LogBroadcaster manages active log subscriptions.
type LogBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[chan *domain.LogEntry]struct{}
	store       store.LogStore
	logChan     chan *domain.LogEntry
	once        sync.Once
}

// GlobalLogBroadcaster is the singleton instance for the application.
var GlobalLogBroadcaster = NewLogBroadcaster()

func NewLogBroadcaster() *LogBroadcaster {
	return &LogBroadcaster{
		subscribers: make(map[chan *domain.LogEntry]struct{}),
		logChan:     make(chan *domain.LogEntry, 4096),
	}
}

func (b *LogBroadcaster) SetStore(s store.LogStore) {
	b.mu.Lock()
	b.store = s
	b.mu.Unlock()

	b.once.Do(func() {
		go b.runPersistenceWorker()
	})
}

func (b *LogBroadcaster) runPersistenceWorker() {
	for entry := range b.logChan {
		b.mu.RLock()
		s := b.store
		b.mu.RUnlock()

		if s != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.Append(ctx, entry)
			cancel()
		}
	}
}

func (b *LogBroadcaster) GetStore() store.LogStore {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.store
}

// StartPruner starts a background goroutine to prune old logs.
func (b *LogBroadcaster) StartPruner(ctx context.Context, interval time.Duration, retention time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.mu.RLock()
				s := b.store
				b.mu.RUnlock()

				if s != nil {
					olderThan := time.Now().Add(-retention)
					count, err := s.Prune(ctx, olderThan)
					if err != nil {
						slog.Error("Failed to prune logs", "error", err)
					} else if count > 0 {
						slog.Info("Pruned old logs", "count", count, "older_than", olderThan)
					}
				}
			}
		}
	}()
}

func (b *LogBroadcaster) Subscribe() chan *domain.LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Use a buffered channel to avoid blocking the logger on slow subscribers
	ch := make(chan *domain.LogEntry, 1024)
	b.subscribers[ch] = struct{}{}
	return ch
}

func (b *LogBroadcaster) Unsubscribe(ch chan *domain.LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		// We don't close the channel here to avoid panics if Broadcast is running.
		// Instead, we just let it be garbage collected or handled by the receiver's context.
	}
}

func (b *LogBroadcaster) Broadcast(entry *domain.LogEntry) {
	// Send to persistence worker non-blocking
	select {
	case b.logChan <- entry:
	default:
		// Drop if buffer full to maintain performance
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- entry:
		default:
			// Subscriber buffer full, drop log to maintain performance
		}
	}
}

// BroadcastHandler is a slog.Handler that forwards logs to a broadcaster.
type BroadcastHandler struct {
	handler     slog.Handler
	broadcaster *LogBroadcaster
}

func NewBroadcastHandler(inner slog.Handler, b *LogBroadcaster) *BroadcastHandler {
	return &BroadcastHandler{
		handler:     inner,
		broadcaster: b,
	}
}

func (h *BroadcastHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *BroadcastHandler) Handle(ctx context.Context, r slog.Record) error {
	// First call the wrapped handler to actually log it (e.g. to stdout)
	if err := h.handler.Handle(ctx, r); err != nil {
		return err
	}

	// Prepare LogEntry for broadcasting
	attrs := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})

	entry := &domain.LogEntry{
		Timestamp:  timestamppb.New(r.Time),
		Level:      r.Level.String(),
		Message:    r.Message,
		Attributes: attrs,
	}

	h.broadcaster.Broadcast(entry)
	return nil
}

func (h *BroadcastHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &BroadcastHandler{
		handler:     h.handler.WithAttrs(attrs),
		broadcaster: h.broadcaster,
	}
}

func (h *BroadcastHandler) WithGroup(name string) slog.Handler {
	return &BroadcastHandler{
		handler:     h.handler.WithGroup(name),
		broadcaster: h.broadcaster,
	}
}

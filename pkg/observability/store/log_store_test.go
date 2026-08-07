package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newTestLogStore(t *testing.T) LogStore {
	t.Helper()
	s, err := NewSQLiteLogStore(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatalf("NewSQLiteLogStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Append used to write the literal "{}" for attributes and GetLogs never
// selected the column, so every structured attribute was silently discarded.
func TestAppendRoundTripsAttributes(t *testing.T) {
	s := newTestLogStore(t)
	ctx := context.Background()

	want := map[string]string{"backend": "10.0.0.1:5432", "trace": "abc123"}
	err := s.Append(ctx, &domain.LogEntry{
		Timestamp:  timestamppb.New(time.Now()),
		Level:      "ERROR",
		Message:    "pool exhausted",
		Attributes: want,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, total, err := s.GetLogs(ctx, LogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("got %d entries (total %d), want 1", len(got), total)
	}
	for key, value := range want {
		if got[0].Attributes[key] != value {
			t.Errorf("attribute %q = %q, want %q", key, got[0].Attributes[key], value)
		}
	}
}

func TestAppendBatchPersistsAllEntries(t *testing.T) {
	s := newTestLogStore(t)
	ctx := context.Background()

	base := time.Now().Add(-time.Hour)
	entries := make([]*domain.LogEntry, 0, 500)
	for i := range 500 {
		entries = append(entries, &domain.LogEntry{
			Timestamp: timestamppb.New(base.Add(time.Duration(i) * time.Second)),
			Level:     "INFO",
			Message:   "query executed",
		})
	}

	if err := s.AppendBatch(ctx, entries); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	_, total, err := s.GetLogs(ctx, LogFilter{Limit: 1})
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if total != 500 {
		t.Fatalf("total = %d, want 500", total)
	}
}

func TestAppendBatchEmptyIsNoop(t *testing.T) {
	s := newTestLogStore(t)
	if err := s.AppendBatch(context.Background(), nil); err != nil {
		t.Fatalf("AppendBatch(nil): %v", err)
	}
}

// A search for "100%" must not be treated as a LIKE prefix wildcard.
func TestGetLogsEscapesLikeWildcards(t *testing.T) {
	s := newTestLogStore(t)
	ctx := context.Background()

	now := time.Now()
	for _, msg := range []string{"cache hit ratio 100% on backend", "unrelated message"} {
		if err := s.Append(ctx, &domain.LogEntry{
			Timestamp: timestamppb.New(now),
			Level:     "INFO",
			Message:   msg,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, total, err := s.GetLogs(ctx, LogFilter{Search: "100%", Limit: 10})
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("got %d entries (total %d), want exactly the literal match", len(got), total)
	}
	if got[0].Message != "cache hit ratio 100% on backend" {
		t.Errorf("matched %q", got[0].Message)
	}
}

func TestGetLogsBoundsLimit(t *testing.T) {
	s := newTestLogStore(t)
	ctx := context.Background()

	entries := make([]*domain.LogEntry, 0, 1200)
	base := time.Now().Add(-time.Hour)
	for i := range 1200 {
		entries = append(entries, &domain.LogEntry{
			Timestamp: timestamppb.New(base.Add(time.Duration(i) * time.Millisecond)),
			Level:     "INFO",
			Message:   "x",
		})
	}
	if err := s.AppendBatch(ctx, entries); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	// A caller asking for everything must still be capped.
	got, _, err := s.GetLogs(ctx, LogFilter{Limit: 0})
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if len(got) > 1000 {
		t.Fatalf("returned %d rows, want <= 1000", len(got))
	}
}

// The level index was dropped for being counterproductive; filtering by level
// must still work.
func TestGetLogsFiltersByLevel(t *testing.T) {
	s := newTestLogStore(t)
	ctx := context.Background()

	now := time.Now()
	for _, level := range []string{"INFO", "ERROR", "INFO", "ERROR", "WARN"} {
		if err := s.Append(ctx, &domain.LogEntry{
			Timestamp: timestamppb.New(now),
			Level:     level,
			Message:   "m",
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	_, total, err := s.GetLogs(ctx, LogFilter{Level: "ERROR", Limit: 10})
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if total != 2 {
		t.Fatalf("ERROR total = %d, want 2", total)
	}
}

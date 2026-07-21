package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

// MetricStore defines the interface for persisting and retrieving metrics.
type MetricStore interface {
	SaveSnapshot(ctx context.Context, snap *domain.MetricSnapshot) error
	GetHistory(ctx context.Context, start, end time.Time) ([]*domain.MetricSnapshot, error)
	SaveTopQueries(ctx context.Context, stats []*domain.TopQuery) error
	GetTopQueries(ctx context.Context, start, end time.Time, limit int) ([]*domain.TopQuery, error)
	Prune(ctx context.Context, olderThan time.Time) (int64, error)
	Close() error
}

type sqliteMetricStore struct {
	db *sql.DB
}

// NewSQLiteMetricStore creates a new SQLite-backed metric store.
func NewSQLiteMetricStore(path string) (MetricStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	// Optimize SQLite for performance
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA cache_size = -2000;
		PRAGMA temp_store = MEMORY;
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set pragmas: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := initMetricSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &sqliteMetricStore{db: db}, nil
}

func initMetricSchema(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS metrics_history (
		timestamp DATETIME PRIMARY KEY,
		rps REAL,
		error_rate REAL,
		latency_ms REAL
	);
	CREATE TABLE IF NOT EXISTS top_queries_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		query TEXT NOT NULL,
		count INTEGER,
		total_time_ms INTEGER,
		max_time_ms INTEGER,
		error_count INTEGER,
		last_seen DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_queries_timestamp ON top_queries_history(timestamp);
	CREATE INDEX IF NOT EXISTS idx_queries_query ON top_queries_history(query);
	`
	_, err := db.Exec(query)
	return err
}

func (s *sqliteMetricStore) SaveSnapshot(ctx context.Context, snap *domain.MetricSnapshot) error {
	query := `INSERT OR REPLACE INTO metrics_history (timestamp, rps, error_rate, latency_ms) VALUES (?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, snap.Timestamp.AsTime().UTC().Format(time.RFC3339), snap.RequestsPerSecond, snap.ErrorRate, snap.LatencyMs)
	return err
}

func (s *sqliteMetricStore) GetHistory(ctx context.Context, start, end time.Time) ([]*domain.MetricSnapshot, error) {
	query := `SELECT timestamp, rps, error_rate, latency_ms FROM metrics_history WHERE timestamp >= ? AND timestamp <= ? ORDER BY timestamp ASC`
	rows, err := s.db.QueryContext(ctx, query, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []*domain.MetricSnapshot
	for rows.Next() {
		var ts time.Time
		var rps, errRate, lat float64
		if err := rows.Scan(&ts, &rps, &errRate, &lat); err != nil {
			return nil, err
		}
		history = append(history, &domain.MetricSnapshot{
			Timestamp:         timestamppb.New(ts),
			RequestsPerSecond: float32(rps),
			ErrorRate:         float32(errRate),
			LatencyMs:         float32(lat),
		})
	}
	return history, nil
}

func (s *sqliteMetricStore) SaveTopQueries(ctx context.Context, stats []*domain.TopQuery) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	query := `INSERT INTO top_queries_history (timestamp, query, count, total_time_ms, max_time_ms, error_count, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, stat := range stats {
		if _, err := stmt.ExecContext(ctx, now, stat.Query, stat.Count, stat.TotalTimeMs, stat.MaxTimeMs, stat.ErrorCount, stat.LastSeen.AsTime().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *sqliteMetricStore) GetTopQueries(ctx context.Context, start, end time.Time, limit int) ([]*domain.TopQuery, error) {
	query := `
		SELECT query, SUM(count), SUM(total_time_ms), MAX(max_time_ms), MAX(last_seen), SUM(error_count)
		FROM top_queries_history
		WHERE timestamp >= ? AND timestamp <= ?
		GROUP BY query
		ORDER BY SUM(count) DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []*domain.TopQuery
	for rows.Next() {
		var q string
		var count, totalTime, maxTime, errCount int64
		var lastSeenStr string
		if err := rows.Scan(&q, &count, &totalTime, &maxTime, &lastSeenStr, &errCount); err != nil {
			return nil, err
		}
		lastSeen, _ := time.Parse(time.RFC3339, lastSeenStr)
		if lastSeen.IsZero() {
			// Fallback if format is different
			lastSeen, _ = time.Parse("2006-01-02 15:04:05.999999999-07:00", lastSeenStr)
		}
		avgTime := int64(0)
		if count > 0 {
			avgTime = totalTime / count
		}
		stats = append(stats, &domain.TopQuery{
			Query:       q,
			Count:       count,
			TotalTimeMs: totalTime,
			MaxTimeMs:   maxTime,
			AvgTimeMs:   avgTime,
			LastSeen:    timestamppb.New(lastSeen),
			ErrorCount:  errCount,
		})
	}
	return stats, nil
}

func (s *sqliteMetricStore) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	ts := olderThan.UTC().Format(time.RFC3339)
	res1, err := s.db.ExecContext(ctx, "DELETE FROM metrics_history WHERE timestamp < ?", ts)
	if err != nil {
		return 0, err
	}
	res2, err := s.db.ExecContext(ctx, "DELETE FROM top_queries_history WHERE timestamp < ?", ts)
	if err != nil {
		return 0, err
	}

	n1, _ := res1.RowsAffected()
	n2, _ := res2.RowsAffected()
	return n1 + n2, nil
}

func (s *sqliteMetricStore) Close() error {
	return s.db.Close()
}

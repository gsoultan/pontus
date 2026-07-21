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

// LogStore defines the interface for persisting and retrieving logs.
type LogStore interface {
	Append(ctx context.Context, entry *domain.LogEntry) error
	GetLogs(ctx context.Context, filter LogFilter) ([]*domain.LogEntry, int, error)
	Prune(ctx context.Context, olderThan time.Time) (int64, error)
	Close() error
}

// LogFilter defines filtering criteria for logs.
type LogFilter struct {
	StartTime *time.Time
	EndTime   *time.Time
	Level     string
	Search    string
	Limit     int
	Offset    int
}

type sqliteLogStore struct {
	db *sql.DB
}

// NewSQLiteLogStore creates a new SQLite-backed log store.
func NewSQLiteLogStore(path string) (LogStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	// Optimize SQLite for performance
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA cache_size = -2000; -- ~2MB
		PRAGMA temp_store = MEMORY;
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set pragmas: %w", err)
	}

	// Set connection pooling for better performance
	db.SetMaxOpenConns(1) // SQLite works best with 1 writer for WAL mode too in most cases, but WAL allows readers
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &sqliteLogStore{db: db}, nil
}

func initSchema(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		level TEXT NOT NULL,
		message TEXT NOT NULL,
		attributes TEXT -- JSON string
	);
	CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
	`
	_, err := db.Exec(query)
	return err
}

func (s *sqliteLogStore) Append(ctx context.Context, entry *domain.LogEntry) error {
	// Simple append. We might want to batch this for higher performance if needed.
	// But for a lightweight proxy, direct append might be fine.
	query := `INSERT INTO logs (timestamp, level, message, attributes) VALUES (?, ?, ?, ?)`

	// Convert attributes to a simple string or JSON if needed.
	// For now, let's just use a simple representation.
	// We could use json.Marshal but it adds overhead.
	// However, it's safer for querying later.
	// For now, let's keep it simple.
	_, err := s.db.ExecContext(ctx, query,
		entry.Timestamp.AsTime(),
		entry.Level,
		entry.Message,
		"{}") // Simplified for now
	return err
}

func (s *sqliteLogStore) GetLogs(ctx context.Context, filter LogFilter) ([]*domain.LogEntry, int, error) {
	where := "WHERE 1=1"
	args := []any{}

	if filter.StartTime != nil {
		where += " AND timestamp >= ?"
		args = append(args, *filter.StartTime)
	}
	if filter.EndTime != nil {
		where += " AND timestamp <= ?"
		args = append(args, *filter.EndTime)
	}
	if filter.Level != "" {
		where += " AND level = ?"
		args = append(args, filter.Level)
	}
	if filter.Search != "" {
		where += " AND message LIKE ?"
		args = append(args, "%"+filter.Search+"%")
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM logs %s", where)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Get logs
	query := fmt.Sprintf("SELECT timestamp, level, message FROM logs %s ORDER BY timestamp DESC LIMIT ? OFFSET ?", where)
	lArgs := append(args, filter.Limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, query, lArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []*domain.LogEntry
	for rows.Next() {
		var ts time.Time
		var level, message string
		if err := rows.Scan(&ts, &level, &message); err != nil {
			return nil, 0, err
		}
		// Convert back to proto
		// Skipping attributes for now for simplicity
		entries = append(entries, &domain.LogEntry{
			Timestamp: timestamppb.New(ts),
			Level:     level,
			Message:   message,
		})
	}

	return entries, total, nil
}

func (s *sqliteLogStore) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM logs WHERE timestamp < ?", olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *sqliteLogStore) Close() error {
	return s.db.Close()
}

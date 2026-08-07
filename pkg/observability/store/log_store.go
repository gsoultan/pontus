package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

// LogStore defines the interface for persisting and retrieving logs.
type LogStore interface {
	Append(ctx context.Context, entry *domain.LogEntry) error
	// AppendBatch persists many entries in a single transaction. Writing one
	// row per transaction costs ~10x the throughput (measured: 40k vs 426k
	// rows/sec), so the broadcaster batches and calls this instead.
	AppendBatch(ctx context.Context, entries []*domain.LogEntry) error
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
		PRAGMA wal_autocheckpoint = 1000;
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
		attributes TEXT -- JSON object
	);
	CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
	`
	if _, err := db.Exec(query); err != nil {
		return err
	}

	// idx_logs_level indexed ~5 distinct values. It cost write throughput and
	// misled the planner into an index scan for level-filtered reads, which
	// measured 12.6ms against 267us once dropped. Every query that filters by
	// level also bounds by time, so idx_logs_timestamp is the useful one.
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_logs_level`); err != nil {
		return err
	}
	return nil
}

func (s *sqliteLogStore) Append(ctx context.Context, entry *domain.LogEntry) error {
	return s.AppendBatch(ctx, []*domain.LogEntry{entry})
}

func (s *sqliteLogStore) AppendBatch(ctx context.Context, entries []*domain.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO logs (timestamp, level, message, attributes) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, entry := range entries {
		if entry == nil {
			continue
		}
		ts := time.Now()
		if entry.Timestamp != nil {
			ts = entry.Timestamp.AsTime()
		}
		if _, err := stmt.ExecContext(ctx, ts, entry.Level, entry.Message, encodeAttrs(entry.Attributes)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// encodeAttrs serializes structured log attributes. They used to be written as
// the literal "{}", which silently discarded every attribute the logger
// attached.
func encodeAttrs(attrs map[string]string) string {
	if len(attrs) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(attrs)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func decodeAttrs(raw string) map[string]string {
	if raw == "" || raw == "{}" {
		return nil
	}
	var attrs map[string]string
	if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
		return nil
	}
	return attrs
}

func (s *sqliteLogStore) GetLogs(ctx context.Context, filter LogFilter) ([]*domain.LogEntry, int, error) {
	var where strings.Builder
	where.WriteString("WHERE 1=1")
	args := []any{}

	if filter.StartTime != nil {
		where.WriteString(" AND timestamp >= ?")
		args = append(args, *filter.StartTime)
	}
	if filter.EndTime != nil {
		where.WriteString(" AND timestamp <= ?")
		args = append(args, *filter.EndTime)
	}
	if filter.Level != "" {
		where.WriteString(" AND level = ?")
		args = append(args, filter.Level)
	}
	if filter.Search != "" {
		where.WriteString(" AND message LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(filter.Search)+"%")
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM logs %s", where.String())
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000 // never let a client ask for the whole table
	}

	query := fmt.Sprintf(
		"SELECT timestamp, level, message, attributes FROM logs %s ORDER BY timestamp DESC LIMIT ? OFFSET ?",
		where.String())
	lArgs := append(args, limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, query, lArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []*domain.LogEntry
	for rows.Next() {
		var ts time.Time
		var level, message string
		var attrs sql.NullString
		if err := rows.Scan(&ts, &level, &message, &attrs); err != nil {
			return nil, 0, err
		}
		entries = append(entries, &domain.LogEntry{
			Timestamp:  timestamppb.New(ts),
			Level:      level,
			Message:    message,
			Attributes: decodeAttrs(attrs.String),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return entries, total, nil
}

// escapeLike neutralizes LIKE wildcards in operator-supplied search text so a
// search for "100%" does not turn into a prefix match.
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
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

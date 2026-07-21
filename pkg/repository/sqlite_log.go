package repository

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/pontus/api/proto/domain"
	pb "google.golang.org/protobuf/proto"
)

// SQLiteLog implements the Log repository using SQLite.
type SQLiteLog struct {
	db *sql.DB
}

// Append adds a log entry to the database.
func (s *SQLiteLog) Append(ctx context.Context, projectID string, entry *domain.LogEntry) error {
	data, err := pb.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	id := uuid.New().String()

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO logs (id, project_id, level, msg, data, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, projectID, entry.Level, entry.Message, data, time.Now())
	if err != nil {
		return fmt.Errorf("failed to append log: %w", err)
	}
	return nil
}

// Stream returns an iterator for streaming logs for a project.
func (s *SQLiteLog) Stream(ctx context.Context, projectID string) iter.Seq[*domain.LogEntry] {
	return func(yield func(*domain.LogEntry) bool) {
		rows, err := s.db.QueryContext(ctx, "SELECT data FROM logs WHERE project_id = ? ORDER BY created_at ASC", projectID)
		if err != nil {
			return
		}
		defer rows.Close()

		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return
			}

			entry := new(domain.LogEntry)
			if err := pb.Unmarshal(data, entry); err != nil {
				continue
			}

			if !yield(entry) {
				return
			}
		}
	}
}

// Prune removes logs older than the specified time.
func (s *SQLiteLog) Prune(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM logs WHERE created_at < ?", before)
	if err != nil {
		return fmt.Errorf("failed to prune logs: %w", err)
	}
	return nil
}

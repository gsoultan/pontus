package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gsoultan/pontus/pkg/observability"
)

// SQLiteMetric implements the Metric repository using SQLite.
type SQLiteMetric struct {
	db *sql.DB
}

// SaveSnapshot saves a metric snapshot to the database.
func (s *SQLiteMetric) SaveSnapshot(ctx context.Context, projectID string, snapshot observability.MetricSnapshot) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO metrics (project_id, rps, error_rate, latency, created_at) VALUES (?, ?, ?, ?, ?)",
		projectID, snapshot.RPS, snapshot.ErrorRate, snapshot.Latency, snapshot.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to save metric snapshot: %w", err)
	}
	return nil
}

// GetHistory returns metric history for a project within a time range.
func (s *SQLiteMetric) GetHistory(ctx context.Context, projectID string, start, end time.Time) ([]observability.MetricSnapshot, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT rps, error_rate, latency, created_at FROM metrics WHERE project_id = ? AND created_at BETWEEN ? AND ? ORDER BY created_at ASC",
		projectID, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}
	defer rows.Close()

	var history []observability.MetricSnapshot
	for rows.Next() {
		var snapshot observability.MetricSnapshot
		if err := rows.Scan(&snapshot.RPS, &snapshot.ErrorRate, &snapshot.Latency, &snapshot.Timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan metric: %w", err)
		}
		history = append(history, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return history, nil
}

// Prune removes metrics older than the specified time.
func (s *SQLiteMetric) Prune(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM metrics WHERE created_at < ?", before)
	if err != nil {
		return fmt.Errorf("failed to prune metrics: %w", err)
	}
	return nil
}

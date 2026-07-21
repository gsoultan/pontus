package repository

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// SQLite implements the repository interfaces using SQLite.
type SQLite struct {
	db *sql.DB
	Project
	Log
	Metric
}

// NewSQLite creates a new SQLite repository.
func NewSQLite(path string) (*SQLite, error) {
	// Enable WAL mode for better concurrency and performance
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	s := new(SQLite)
	s.db = db
	s.Project = &SQLiteProject{db: db}
	s.Log = &SQLiteLog{db: db}
	s.Metric = &SQLiteMetric{db: db}

	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

func (s *SQLite) init() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS logs (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			level TEXT,
			msg TEXT,
			data BLOB,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_project_created ON logs(project_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS metrics (
			project_id TEXT,
			rps REAL,
			error_rate REAL,
			latency REAL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_project_created ON metrics(project_id, created_at)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to initialize schema: %w", err)
		}
	}
	return nil
}

// Close closes the database connection.
func (s *SQLite) Close() error {
	return s.db.Close()
}

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/gsoultan/pontus/server/management/service"
)

// SQLiteSetting implements SettingProvider using SQLite.
type SQLiteSetting struct {
	db *sql.DB
}

// NewSQLiteSetting creates a new SQLiteSetting.
func NewSQLiteSetting(db *sql.DB) *SQLiteSetting {
	return &SQLiteSetting{db: db}
}

func (s *SQLiteSetting) init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (s *SQLiteSetting) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get setting %s: %w", key, err)
	}
	return value, nil
}

func (s *SQLiteSetting) Set(ctx context.Context, key string, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set setting %s: %w", key, err)
	}
	return nil
}

func (s *SQLiteSetting) List(ctx context.Context) ([]service.Setting, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("failed to list settings: %w", err)
	}
	defer rows.Close()

	var settings []service.Setting
	for rows.Next() {
		var set service.Setting
		if err := rows.Scan(&set.Key, &set.Value); err != nil {
			return nil, fmt.Errorf("failed to scan setting: %w", err)
		}
		settings = append(settings, set)
	}
	return settings, nil
}

func (s *SQLiteSetting) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("failed to delete setting %s: %w", key, err)
	}
	return nil
}

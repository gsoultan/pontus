package store

import (
	"database/sql"
	"strings"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/auth"
)

type sqliteUserStore struct {
	db *sql.DB
}

// NewSQLiteUser creates a new SQLite-backed user store.
func NewSQLiteUser(db *sql.DB) User {
	return new(sqliteUserStore{db: db})
}

func (s *sqliteUserStore) init() error {
	// Migration: Rename password to password_hash if it exists
	_, _ = s.db.Exec("ALTER TABLE users RENAME COLUMN password TO password_hash")

	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			username TEXT PRIMARY KEY,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL
		)
	`)
	return err
}

func (s *sqliteUserStore) List() []*endpoints.LoginResponse {
	rows, err := s.db.Query("SELECT username, password_hash, role FROM users")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var users []*endpoints.LoginResponse
	for rows.Next() {
		var u endpoints.LoginResponse
		if err := rows.Scan(&u.Username, &u.Token, &u.Role); err == nil {
			users = append(users, &u)
		}
	}
	return users
}

func (s *sqliteUserStore) Get(username string) (*endpoints.LoginResponse, bool) {
	var u endpoints.LoginResponse
	err := s.db.QueryRow("SELECT username, password_hash, role FROM users WHERE username = ?", username).Scan(&u.Username, &u.Token, &u.Role)
	if err != nil {
		return nil, false
	}
	return &u, true
}

func (s *sqliteUserStore) Upsert(username, password, role string) error {
	// Ensure password is hashed
	hashedPassword := password
	if !strings.HasPrefix(password, "$2a$") && !strings.HasPrefix(password, "$2b$") && !strings.HasPrefix(password, "$2y$") {
		var err error
		hashedPassword, err = auth.HashPassword(password)
		if err != nil {
			return err
		}
	}

	_, err := s.db.Exec("INSERT OR REPLACE INTO users (username, password_hash, role) VALUES (?, ?, ?)", username, hashedPassword, role)
	return err
}

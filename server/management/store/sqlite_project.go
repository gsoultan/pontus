package store

import (
	"database/sql"
	"encoding/json"

	"github.com/gsoultan/pontus/api/proto/domain"
)

type projectStore struct {
	db *sql.DB
}

// NewSQLiteProject creates a new SQLite-backed project store.
func NewSQLiteProject(db *sql.DB) Project {
	return &projectStore{db: db}
}

func (s *projectStore) init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		)
	`)
	return err
}

func (s *projectStore) List() []*domain.Project {
	rows, err := s.db.Query("SELECT data FROM projects")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var projects []*domain.Project
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err == nil {
			var p domain.Project
			if err := json.Unmarshal([]byte(data), &p); err == nil {
				projects = append(projects, &p)
			}
		}
	}
	return projects
}

func (s *projectStore) Get(id string) (*domain.Project, bool) {
	var data string
	err := s.db.QueryRow("SELECT data FROM projects WHERE id = ?", id).Scan(&data)
	if err != nil {
		return nil, false
	}

	var p domain.Project
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, false
	}
	return &p, true
}

func (s *projectStore) Upsert(p *domain.Project) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT OR REPLACE INTO projects (id, data) VALUES (?, ?)", p.Id, string(data))
	return err
}

func (s *projectStore) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM projects WHERE id = ?", id)
	return err
}

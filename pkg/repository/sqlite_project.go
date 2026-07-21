package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/gsoultan/pontus/api/proto/domain"
	pb "google.golang.org/protobuf/proto"
)

// SQLiteProject implements the Project repository using SQLite.
type SQLiteProject struct {
	db *sql.DB
}

// List returns all projects from the database.
func (s *SQLiteProject) List(ctx context.Context) ([]*domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM projects")
	if err != nil {
		return nil, fmt.Errorf("failed to query projects: %w", err)
	}
	defer rows.Close()

	var projects []*domain.Project
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("failed to scan project data: %w", err)
		}

		p := new(domain.Project)
		if err := pb.Unmarshal(data, p); err != nil {
			return nil, fmt.Errorf("failed to unmarshal project: %w", err)
		}
		projects = append(projects, p)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return projects, nil
}

// Get returns a project by ID.
func (s *SQLiteProject) Get(ctx context.Context, id string) (*domain.Project, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM projects WHERE id = ?", id).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	p := new(domain.Project)
	if err := pb.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("failed to unmarshal project: %w", err)
	}
	return p, nil
}

// Upsert inserts or updates a project.
func (s *SQLiteProject) Upsert(ctx context.Context, p *domain.Project) error {
	data, err := pb.Marshal(p)
	if err != nil {
		return fmt.Errorf("failed to marshal project: %w", err)
	}

	_, err = s.db.ExecContext(ctx, "INSERT INTO projects (id, data) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data", p.Id, data)
	if err != nil {
		return fmt.Errorf("failed to upsert project: %w", err)
	}
	return nil
}

// Delete removes a project by ID.
func (s *SQLiteProject) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete project: %w", err)
	}
	return nil
}

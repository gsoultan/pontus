package repository

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/domain"
)

// Project defines the persistence operations for projects.
type Project interface {
	List(ctx context.Context) ([]*domain.Project, error)
	Get(ctx context.Context, id string) (*domain.Project, error)
	Upsert(ctx context.Context, p *domain.Project) error
	Delete(ctx context.Context, id string) error
}

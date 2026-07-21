package store

import (
	"github.com/gsoultan/pontus/api/proto/domain"
)

// Project defines the interface for project configuration persistence.
type Project interface {
	List() []*domain.Project
	Get(id string) (*domain.Project, bool)
	Upsert(p *domain.Project) error
	Delete(id string) error
}

package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Project defines the interface for project management.
type Project interface {
	ListProjects(ctx context.Context) (*endpoints.ListProjectsResponse, error)
	CreateProject(ctx context.Context, req *endpoints.CreateProjectRequest) (*endpoints.CreateProjectResponse, error)
	DeleteProject(ctx context.Context, req *endpoints.DeleteProjectRequest) (*endpoints.DeleteProjectResponse, error)
}

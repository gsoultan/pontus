package manager

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Project implements ProjectService.
type Project struct {
	registry *registry.Registry
}

func NewProject(registry *registry.Registry) *Project {
	return &Project{registry: registry}
}

func (m *Project) ListProjects(ctx context.Context) (*endpoints.ListProjectsResponse, error) {
	return &endpoints.ListProjectsResponse{
		Projects: m.registry.ListProjects(),
	}, nil
}

func (m *Project) CreateProject(ctx context.Context, req *endpoints.CreateProjectRequest) (*endpoints.CreateProjectResponse, error) {
	projectID := uuid.New().String()

	pcfg := req.Project
	if pcfg == nil {
		pcfg = &domain.Project{}
	}

	pcfg.Id = projectID
	pcfg.CreatedAt = timestamppb.Now()

	// Ensure at least one proxy exists if it was provided in some way,
	// or if the client already filled the proxies list.
	for _, p := range pcfg.Proxies {
		if p.Id == "" {
			p.Id = uuid.New().String()
		}
	}

	state, err := m.registry.CreateProjectState(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create project state: %w", err)
	}

	if err := m.registry.UpsertProject(pcfg); err != nil {
		state.Stop()
		return nil, fmt.Errorf("failed to save project: %w", err)
	}

	m.registry.AddProject(projectID, state)
	return &endpoints.CreateProjectResponse{Project: pcfg}, nil
}

func (m *Project) DeleteProject(ctx context.Context, req *endpoints.DeleteProjectRequest) (*endpoints.DeleteProjectResponse, error) {
	state, err := m.registry.GetProjectState(req.Id)
	if err != nil {
		return nil, err
	}

	state.Stop()
	m.registry.RemoveProject(req.Id)
	if err := m.registry.DeleteProject(req.Id); err != nil {
		return nil, err
	}

	return &endpoints.DeleteProjectResponse{}, nil
}

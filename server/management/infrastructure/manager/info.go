package manager

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/version"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/service"
)

type infoManager struct {
	registry *registry.Registry
}

// NewInfo creates a new info manager.
func NewInfo(r *registry.Registry) service.Info {
	return &infoManager{
		registry: r,
	}
}

func (m *infoManager) GetServerInfo(ctx context.Context, req *endpoints.GetServerInfoRequest) (*endpoints.GetServerInfoResponse, error) {
	return &endpoints.GetServerInfoResponse{
		Version:   version.Version,
		Commit:    version.Commit,
		BuildTime: version.BuildTime,
	}, nil
}

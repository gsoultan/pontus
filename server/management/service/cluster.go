package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Cluster defines the interface for cluster-wide configurations.
type Cluster interface {
	SetClusterConfig(ctx context.Context, req *endpoints.SetClusterConfigRequest) (*endpoints.SetClusterConfigResponse, error)
	GetClusterConfig(ctx context.Context, req *endpoints.GetClusterConfigRequest) (*endpoints.GetClusterConfigResponse, error)
	DiscoverCluster(ctx context.Context, req *endpoints.DiscoverClusterRequest) (*endpoints.DiscoverClusterResponse, error)
}

package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Backend defines the interface for backend management.
type Backend interface {
	AddBackend(ctx context.Context, req *endpoints.AddBackendRequest) (*endpoints.AddBackendResponse, error)
	RemoveBackend(ctx context.Context, req *endpoints.RemoveBackendRequest) (*endpoints.RemoveBackendResponse, error)
	UpdateBackend(ctx context.Context, req *endpoints.UpdateBackendRequest) (*endpoints.UpdateBackendResponse, error)
	ProvisionReplica(ctx context.Context, req *endpoints.ProvisionReplicaRequest, progress chan<- *endpoints.ProvisionProgress) error
	ValidateBackend(ctx context.Context, req *endpoints.ValidateBackendRequest) (*endpoints.ValidateBackendResponse, error)
	InitializeNode(ctx context.Context, req *endpoints.InitializeNodeRequest, progress chan<- *endpoints.InitializeNodeProgress) error
	InstallNode(ctx context.Context, req *endpoints.InstallNodeRequest, progress chan<- *endpoints.InstallNodeProgress) error
	BackupBackend(ctx context.Context, req *endpoints.BackupBackendRequest, progress chan<- *endpoints.BackupBackendProgress) error
	RestoreBackend(ctx context.Context, req *endpoints.RestoreBackendRequest, progress chan<- *endpoints.RestoreBackendProgress) error
	PromoteBackend(ctx context.Context, req *endpoints.PromoteBackendRequest) (*endpoints.PromoteBackendResponse, error)
	DiscoverCluster(ctx context.Context, req *endpoints.DiscoverClusterRequest) (*endpoints.DiscoverClusterResponse, error)
	VacuumBackend(ctx context.Context, req *endpoints.VacuumBackendRequest, progress chan<- *endpoints.VacuumBackendProgress) error
	GetAgentInfo(ctx context.Context, req *endpoints.GetAgentInfoRequest) (*endpoints.GetAgentInfoResponse, error)
	GetAvailableVersions(ctx context.Context, req *endpoints.GetAvailableVersionsRequest) (*endpoints.GetAvailableVersionsResponse, error)
	RestartBackendService(ctx context.Context, req *endpoints.RestartBackendServiceRequest) (*endpoints.RestartBackendServiceResponse, error)
	ShutdownBackend(ctx context.Context, req *endpoints.ShutdownBackendRequest) (*endpoints.ShutdownBackendResponse, error)
}

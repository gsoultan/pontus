package management

import (
	"context"

	"github.com/go-kit/kit/endpoint"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/management/service"
)

// Endpoints holds all Go kit endpoints for the management service.
type Endpoints struct {
	ListProjectsEndpoint               endpoint.Endpoint
	CreateProjectEndpoint              endpoint.Endpoint
	DeleteProjectEndpoint              endpoint.Endpoint
	GetStatusEndpoint                  endpoint.Endpoint
	AddBackendEndpoint                 endpoint.Endpoint
	RemoveBackendEndpoint              endpoint.Endpoint
	UpdateBackendEndpoint              endpoint.Endpoint
	ValidateBackendEndpoint            endpoint.Endpoint
	AddProxyEndpoint                   endpoint.Endpoint
	RemoveProxyEndpoint                endpoint.Endpoint
	UpdateProxyEndpoint                endpoint.Endpoint
	ProvisionReplicaEndpoint           endpoint.Endpoint
	InitializeNodeEndpoint             endpoint.Endpoint
	InstallNodeEndpoint                endpoint.Endpoint
	BackupBackendEndpoint              endpoint.Endpoint
	RestoreBackendEndpoint             endpoint.Endpoint
	PromoteBackendEndpoint             endpoint.Endpoint
	ListReplicationStreamsEndpoint     endpoint.Endpoint
	TerminateReplicationStreamEndpoint endpoint.Endpoint
	CreateLogicalSlotEndpoint          endpoint.Endpoint
	StreamLogsEndpoint                 endpoint.Endpoint
	ExplainQueryEndpoint               endpoint.Endpoint
	SetClusterConfigEndpoint           endpoint.Endpoint
	GetClusterConfigEndpoint           endpoint.Endpoint
	DiscoverClusterEndpoint            endpoint.Endpoint
	VacuumBackendEndpoint              endpoint.Endpoint
	GetLogsEndpoint                    endpoint.Endpoint
	GetMetricsHistoryEndpoint          endpoint.Endpoint
	GetTopQueriesHistoryEndpoint       endpoint.Endpoint
	TuneDatabaseEndpoint               endpoint.Endpoint
	ApplyTuningEndpoint                endpoint.Endpoint
	GetAgentInfoEndpoint               endpoint.Endpoint
	GetAvailableVersionsEndpoint       endpoint.Endpoint
	LoginEndpoint                      endpoint.Endpoint
	CreateUserEndpoint                 endpoint.Endpoint
	GetServerInfoEndpoint              endpoint.Endpoint
	GetPostgresInsightsEndpoint        endpoint.Endpoint
	RestartBackendServiceEndpoint      endpoint.Endpoint
	ShutdownBackendEndpoint            endpoint.Endpoint
}

// MakeEndpoints returns an Endpoints struct where each field is an endpoint.
func MakeEndpoints(s service.Service) Endpoints {
	return Endpoints{
		ListProjectsEndpoint:               makeListProjectsEndpoint(s),
		CreateProjectEndpoint:              makeCreateProjectEndpoint(s),
		DeleteProjectEndpoint:              makeDeleteProjectEndpoint(s),
		GetStatusEndpoint:                  makeGetStatusEndpoint(s),
		AddBackendEndpoint:                 makeAddBackendEndpoint(s),
		RemoveBackendEndpoint:              makeRemoveBackendEndpoint(s),
		UpdateBackendEndpoint:              makeUpdateBackendEndpoint(s),
		ValidateBackendEndpoint:            makeValidateBackendEndpoint(s),
		AddProxyEndpoint:                   makeAddProxyEndpoint(s),
		RemoveProxyEndpoint:                makeRemoveProxyEndpoint(s),
		UpdateProxyEndpoint:                makeUpdateProxyEndpoint(s),
		ProvisionReplicaEndpoint:           makeProvisionReplicaEndpoint(s),
		InitializeNodeEndpoint:             makeInitializeNodeEndpoint(s),
		InstallNodeEndpoint:                makeInstallNodeEndpoint(s),
		BackupBackendEndpoint:              makeBackupBackendEndpoint(s),
		RestoreBackendEndpoint:             makeRestoreBackendEndpoint(s),
		PromoteBackendEndpoint:             makePromoteBackendEndpoint(s),
		ListReplicationStreamsEndpoint:     makeListReplicationStreamsEndpoint(s),
		TerminateReplicationStreamEndpoint: makeTerminateReplicationStreamEndpoint(s),
		CreateLogicalSlotEndpoint:          makeCreateLogicalSlotEndpoint(s),
		StreamLogsEndpoint:                 makeStreamLogsEndpoint(s),
		ExplainQueryEndpoint:               makeExplainQueryEndpoint(s),
		SetClusterConfigEndpoint:           makeSetClusterConfigEndpoint(s),
		GetClusterConfigEndpoint:           makeGetClusterConfigEndpoint(s),
		DiscoverClusterEndpoint:            makeDiscoverClusterEndpoint(s),
		VacuumBackendEndpoint:              makeVacuumBackendEndpoint(s),
		GetLogsEndpoint:                    makeGetLogsEndpoint(s),
		GetMetricsHistoryEndpoint:          makeGetMetricsHistoryEndpoint(s),
		GetTopQueriesHistoryEndpoint:       makeGetTopQueriesHistoryEndpoint(s),
		TuneDatabaseEndpoint:               makeTuneDatabaseEndpoint(s),
		ApplyTuningEndpoint:                makeApplyTuningEndpoint(s),
		GetAgentInfoEndpoint:               makeGetAgentInfoEndpoint(s),
		GetAvailableVersionsEndpoint:       makeGetAvailableVersionsEndpoint(s),
		LoginEndpoint:                      makeLoginEndpoint(s),
		CreateUserEndpoint:                 makeCreateUserEndpoint(s),
		GetServerInfoEndpoint:              makeGetServerInfoEndpoint(s),
		GetPostgresInsightsEndpoint:        makeGetPostgresInsightsEndpoint(s),
		RestartBackendServiceEndpoint:      makeRestartBackendServiceEndpoint(s),
		ShutdownBackendEndpoint:            makeShutdownBackendEndpoint(s),
	}
}

func makeListProjectsEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		return s.ListProjects(ctx)
	}
}

func makeCreateProjectEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.CreateProjectRequest)
		return s.CreateProject(ctx, req)
	}
}

func makeDeleteProjectEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.DeleteProjectRequest)
		return s.DeleteProject(ctx, req)
	}
}

func makeGetStatusEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetStatusRequest)
		return s.GetStatus(ctx, req)
	}
}

func makeAddBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.AddBackendRequest)
		return s.AddBackend(ctx, req)
	}
}

func makeRemoveBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.RemoveBackendRequest)
		return s.RemoveBackend(ctx, req)
	}
}

func makeUpdateBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.UpdateBackendRequest)
		return s.UpdateBackend(ctx, req)
	}
}

func makeValidateBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.ValidateBackendRequest)
		return s.ValidateBackend(ctx, req)
	}
}

func makeAddProxyEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.AddProxyRequest)
		return s.AddProxy(ctx, req)
	}
}

func makeRemoveProxyEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.RemoveProxyRequest)
		return s.RemoveProxy(ctx, req)
	}
}

func makeUpdateProxyEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.UpdateProxyRequest)
		return s.UpdateProxy(ctx, req)
	}
}

type ProvisionReplicaRequest struct {
	Req      *endpoints.ProvisionReplicaRequest
	Progress chan<- *endpoints.ProvisionProgress
}

func makeProvisionReplicaEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(ProvisionReplicaRequest)
		return nil, s.ProvisionReplica(ctx, req.Req, req.Progress)
	}
}

type InitializeNodeRequest struct {
	Req      *endpoints.InitializeNodeRequest
	Progress chan<- *endpoints.InitializeNodeProgress
}

func makeInitializeNodeEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(InitializeNodeRequest)
		return nil, s.InitializeNode(ctx, req.Req, req.Progress)
	}
}

type InstallNodeRequest struct {
	Req      *endpoints.InstallNodeRequest
	Progress chan<- *endpoints.InstallNodeProgress
}

func makeInstallNodeEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(InstallNodeRequest)
		return nil, s.InstallNode(ctx, req.Req, req.Progress)
	}
}

type BackupBackendRequest struct {
	Req      *endpoints.BackupBackendRequest
	Progress chan<- *endpoints.BackupBackendProgress
}

func makeBackupBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(BackupBackendRequest)
		return nil, s.BackupBackend(ctx, req.Req, req.Progress)
	}
}

type RestoreBackendRequest struct {
	Req      *endpoints.RestoreBackendRequest
	Progress chan<- *endpoints.RestoreBackendProgress
}

func makeRestoreBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(RestoreBackendRequest)
		return nil, s.RestoreBackend(ctx, req.Req, req.Progress)
	}
}

func makePromoteBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.PromoteBackendRequest)
		return s.PromoteBackend(ctx, req)
	}
}

type StreamLogsRequest struct {
	Req  *endpoints.StreamLogsRequest
	Logs chan<- *domain.LogEntry
}

func makeStreamLogsEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(StreamLogsRequest)
		return nil, s.StreamLogs(ctx, req.Req, req.Logs)
	}
}

func makeExplainQueryEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.ExplainQueryRequest)
		return s.ExplainQuery(ctx, req)
	}
}

func makeSetClusterConfigEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.SetClusterConfigRequest)
		return s.SetClusterConfig(ctx, req)
	}
}

func makeGetClusterConfigEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetClusterConfigRequest)
		return s.GetClusterConfig(ctx, req)
	}
}

func makeDiscoverClusterEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.DiscoverClusterRequest)
		return s.DiscoverCluster(ctx, req)
	}
}

type VacuumBackendRequest struct {
	Req      *endpoints.VacuumBackendRequest
	Progress chan<- *endpoints.VacuumBackendProgress
}

func makeVacuumBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*VacuumBackendRequest)
		return nil, s.VacuumBackend(ctx, req.Req, req.Progress)
	}
}

func makeGetLogsEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetLogsRequest)
		return s.GetLogs(ctx, req)
	}
}

func makeGetMetricsHistoryEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetMetricsHistoryRequest)
		return s.GetMetricsHistory(ctx, req)
	}
}

func makeGetTopQueriesHistoryEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetTopQueriesHistoryRequest)
		return s.GetTopQueriesHistory(ctx, req)
	}
}

func makeTuneDatabaseEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.TuneDatabaseRequest)
		return s.TuneDatabase(ctx, req)
	}
}

func makeApplyTuningEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.ApplyTuningRequest)
		return s.ApplyTuning(ctx, req)
	}
}

func makeGetAgentInfoEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetAgentInfoRequest)
		return s.GetAgentInfo(ctx, req)
	}
}

func makeGetAvailableVersionsEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetAvailableVersionsRequest)
		return s.GetAvailableVersions(ctx, req)
	}
}

func makeLoginEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.LoginRequest)
		return s.Login(ctx, req)
	}
}

func makeCreateUserEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.CreateUserRequest)
		return s.CreateUser(ctx, req)
	}
}

func makeGetServerInfoEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetServerInfoRequest)
		return s.GetServerInfo(ctx, req)
	}
}

func makeGetPostgresInsightsEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetBackendPostgresInsightsRequest)
		return s.GetPostgresInsights(ctx, req)
	}
}

func makeRestartBackendServiceEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.RestartBackendServiceRequest)
		return s.RestartBackendService(ctx, req)
	}
}

func makeShutdownBackendEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.ShutdownBackendRequest)
		return s.ShutdownBackend(ctx, req)
	}
}

func makeListReplicationStreamsEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		return s.ListStreams(ctx, request.(*endpoints.ListReplicationStreamsRequest))
	}
}

func makeTerminateReplicationStreamEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		return s.TerminateStream(ctx, request.(*endpoints.TerminateReplicationStreamRequest))
	}
}

func makeCreateLogicalSlotEndpoint(s service.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		return s.CreateLogicalSlot(ctx, request.(*endpoints.CreateLogicalSlotRequest))
	}
}

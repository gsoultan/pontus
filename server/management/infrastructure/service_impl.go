package infrastructure

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/auth"
	"github.com/gsoultan/pontus/server/management/infrastructure/manager"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/service"
	"github.com/gsoultan/pontus/server/management/store"
)

type serviceImpl struct {
	Registry             *registry.Registry
	ProjectService       service.Project
	ProxyService         service.Proxy
	BackendService       service.Backend
	ObservabilityService service.Observability
	ClusterService       service.Cluster
	AuthService          service.Auth
	InfoService          service.Info
}

// NewService creates a new management service.
func NewService(ctx context.Context, projectStore store.Project, userStore store.User, settingStore service.SettingProvider, dialTimeout time.Duration, backendTLS *tls.Config, issuer *auth.Issuer) service.Service {
	r := registry.NewRegistry(ctx, projectStore, userStore, dialTimeout, backendTLS)

	return new(serviceImpl{
		Registry:             r,
		ProjectService:       manager.NewProject(r),
		ProxyService:         manager.NewProxy(r),
		BackendService:       manager.NewBackend(r),
		ObservabilityService: manager.NewObservability(r),
		ClusterService:       manager.NewCluster(r, settingStore),
		AuthService:          manager.NewAuth(r, issuer),
		InfoService:          manager.NewInfo(r),
	})
}

func (s *serviceImpl) ListProjects(ctx context.Context) (*endpoints.ListProjectsResponse, error) {
	return s.ProjectService.ListProjects(ctx)
}

func (s *serviceImpl) CreateProject(ctx context.Context, req *endpoints.CreateProjectRequest) (*endpoints.CreateProjectResponse, error) {
	return s.ProjectService.CreateProject(ctx, req)
}

func (s *serviceImpl) DeleteProject(ctx context.Context, req *endpoints.DeleteProjectRequest) (*endpoints.DeleteProjectResponse, error) {
	return s.ProjectService.DeleteProject(ctx, req)
}

func (s *serviceImpl) AddProxy(ctx context.Context, req *endpoints.AddProxyRequest) (*endpoints.AddProxyResponse, error) {
	return s.ProxyService.AddProxy(ctx, req)
}

func (s *serviceImpl) RemoveProxy(ctx context.Context, req *endpoints.RemoveProxyRequest) (*endpoints.RemoveProxyResponse, error) {
	return s.ProxyService.RemoveProxy(ctx, req)
}

func (s *serviceImpl) UpdateProxy(ctx context.Context, req *endpoints.UpdateProxyRequest) (*endpoints.UpdateProxyResponse, error) {
	return s.ProxyService.UpdateProxy(ctx, req)
}

func (s *serviceImpl) GetStatus(ctx context.Context, req *endpoints.GetStatusRequest) (*endpoints.GetStatusResponse, error) {
	return s.ObservabilityService.GetStatus(ctx, req)
}

func (s *serviceImpl) AddBackend(ctx context.Context, req *endpoints.AddBackendRequest) (*endpoints.AddBackendResponse, error) {
	return s.BackendService.AddBackend(ctx, req)
}

func (s *serviceImpl) RemoveBackend(ctx context.Context, req *endpoints.RemoveBackendRequest) (*endpoints.RemoveBackendResponse, error) {
	return s.BackendService.RemoveBackend(ctx, req)
}

func (s *serviceImpl) UpdateBackend(ctx context.Context, req *endpoints.UpdateBackendRequest) (*endpoints.UpdateBackendResponse, error) {
	return s.BackendService.UpdateBackend(ctx, req)
}

func (s *serviceImpl) ProvisionReplica(ctx context.Context, req *endpoints.ProvisionReplicaRequest, progress chan<- *endpoints.ProvisionProgress) error {
	return s.BackendService.ProvisionReplica(ctx, req, progress)
}

func (s *serviceImpl) ValidateBackend(ctx context.Context, req *endpoints.ValidateBackendRequest) (*endpoints.ValidateBackendResponse, error) {
	return s.BackendService.ValidateBackend(ctx, req)
}

func (s *serviceImpl) StreamLogs(ctx context.Context, req *endpoints.StreamLogsRequest, logs chan<- *domain.LogEntry) error {
	return s.ObservabilityService.StreamLogs(ctx, req, logs)
}

func (s *serviceImpl) Explain(ctx context.Context, projectID string, query string) (string, error) {
	return s.ObservabilityService.Explain(ctx, projectID, query)
}

func (s *serviceImpl) ExplainQuery(ctx context.Context, req *endpoints.ExplainQueryRequest) (*endpoints.ExplainQueryResponse, error) {
	return s.ObservabilityService.ExplainQuery(ctx, req)
}

func (s *serviceImpl) GetLogs(ctx context.Context, req *endpoints.GetLogsRequest) (*endpoints.GetLogsResponse, error) {
	return s.ObservabilityService.GetLogs(ctx, req)
}

func (s *serviceImpl) GetMetricsHistory(ctx context.Context, req *endpoints.GetMetricsHistoryRequest) (*endpoints.GetMetricsHistoryResponse, error) {
	return s.ObservabilityService.GetMetricsHistory(ctx, req)
}

func (s *serviceImpl) GetTopQueriesHistory(ctx context.Context, req *endpoints.GetTopQueriesHistoryRequest) (*endpoints.GetTopQueriesHistoryResponse, error) {
	return s.ObservabilityService.GetTopQueriesHistory(ctx, req)
}

func (s *serviceImpl) TuneDatabase(ctx context.Context, req *endpoints.TuneDatabaseRequest) (*endpoints.TuneDatabaseResponse, error) {
	return s.ObservabilityService.TuneDatabase(ctx, req)
}

func (s *serviceImpl) ApplyTuning(ctx context.Context, req *endpoints.ApplyTuningRequest) (*endpoints.ApplyTuningResponse, error) {
	return s.ObservabilityService.ApplyTuning(ctx, req)
}

func (s *serviceImpl) InitializeNode(ctx context.Context, req *endpoints.InitializeNodeRequest, progress chan<- *endpoints.InitializeNodeProgress) error {
	return s.BackendService.InitializeNode(ctx, req, progress)
}

func (s *serviceImpl) InstallNode(ctx context.Context, req *endpoints.InstallNodeRequest, progress chan<- *endpoints.InstallNodeProgress) error {
	return s.BackendService.InstallNode(ctx, req, progress)
}

func (s *serviceImpl) BackupBackend(ctx context.Context, req *endpoints.BackupBackendRequest, progress chan<- *endpoints.BackupBackendProgress) error {
	return s.BackendService.BackupBackend(ctx, req, progress)
}

func (s *serviceImpl) RestoreBackend(ctx context.Context, req *endpoints.RestoreBackendRequest, progress chan<- *endpoints.RestoreBackendProgress) error {
	return s.BackendService.RestoreBackend(ctx, req, progress)
}

func (s *serviceImpl) PromoteBackend(ctx context.Context, req *endpoints.PromoteBackendRequest) (*endpoints.PromoteBackendResponse, error) {
	return s.BackendService.PromoteBackend(ctx, req)
}

func (s *serviceImpl) DiscoverCluster(ctx context.Context, req *endpoints.DiscoverClusterRequest) (*endpoints.DiscoverClusterResponse, error) {
	return s.BackendService.DiscoverCluster(ctx, req)
}

func (s *serviceImpl) VacuumBackend(ctx context.Context, req *endpoints.VacuumBackendRequest, progress chan<- *endpoints.VacuumBackendProgress) error {
	return s.BackendService.VacuumBackend(ctx, req, progress)
}

func (s *serviceImpl) SetClusterConfig(ctx context.Context, req *endpoints.SetClusterConfigRequest) (*endpoints.SetClusterConfigResponse, error) {
	return s.ClusterService.SetClusterConfig(ctx, req)
}
func (s *serviceImpl) GetClusterConfig(ctx context.Context, req *endpoints.GetClusterConfigRequest) (*endpoints.GetClusterConfigResponse, error) {
	return s.ClusterService.GetClusterConfig(ctx, req)
}

func (s *serviceImpl) GetAgentInfo(ctx context.Context, req *endpoints.GetAgentInfoRequest) (*endpoints.GetAgentInfoResponse, error) {
	return s.BackendService.GetAgentInfo(ctx, req)
}

func (s *serviceImpl) GetAvailableVersions(ctx context.Context, req *endpoints.GetAvailableVersionsRequest) (*endpoints.GetAvailableVersionsResponse, error) {
	return s.BackendService.GetAvailableVersions(ctx, req)
}

func (s *serviceImpl) Login(ctx context.Context, req *endpoints.LoginRequest) (*endpoints.LoginResponse, error) {
	return s.AuthService.Login(ctx, req)
}

func (s *serviceImpl) CreateUser(ctx context.Context, req *endpoints.CreateUserRequest) (*endpoints.CreateUserResponse, error) {
	return s.AuthService.CreateUser(ctx, req)
}

func (s *serviceImpl) GetServerInfo(ctx context.Context, req *endpoints.GetServerInfoRequest) (*endpoints.GetServerInfoResponse, error) {
	return s.InfoService.GetServerInfo(ctx, req)
}

func (s *serviceImpl) GetPostgresInsights(ctx context.Context, req *endpoints.GetBackendPostgresInsightsRequest) (*endpoints.GetBackendPostgresInsightsResponse, error) {
	return s.ObservabilityService.GetPostgresInsights(ctx, req)
}

func (s *serviceImpl) RestartBackendService(ctx context.Context, req *endpoints.RestartBackendServiceRequest) (*endpoints.RestartBackendServiceResponse, error) {
	return s.BackendService.RestartBackendService(ctx, req)
}

func (s *serviceImpl) ShutdownBackend(ctx context.Context, req *endpoints.ShutdownBackendRequest) (*endpoints.ShutdownBackendResponse, error) {
	return s.BackendService.ShutdownBackend(ctx, req)
}

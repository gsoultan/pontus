package handler

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service/serviceconnect"
	"github.com/gsoultan/pontus/server/management"
)

type managementHandler struct {
	endpoints management.Endpoints
}

func NewManagementHandler(endpoints management.Endpoints) serviceconnect.ManagementServiceHandler {
	return &managementHandler{
		endpoints: endpoints,
	}
}

func (h *managementHandler) ListProjects(
	ctx context.Context,
	req *connect.Request[endpoints.ListProjectsRequest],
) (*connect.Response[endpoints.ListProjectsResponse], error) {
	resp, err := h.endpoints.ListProjectsEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.ListProjectsResponse)), nil
}

func (h *managementHandler) CreateProject(
	ctx context.Context,
	req *connect.Request[endpoints.CreateProjectRequest],
) (*connect.Response[endpoints.CreateProjectResponse], error) {
	resp, err := h.endpoints.CreateProjectEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.CreateProjectResponse)), nil
}

func (h *managementHandler) DeleteProject(
	ctx context.Context,
	req *connect.Request[endpoints.DeleteProjectRequest],
) (*connect.Response[endpoints.DeleteProjectResponse], error) {
	resp, err := h.endpoints.DeleteProjectEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.DeleteProjectResponse)), nil
}

func (h *managementHandler) AddProxy(
	ctx context.Context,
	req *connect.Request[endpoints.AddProxyRequest],
) (*connect.Response[endpoints.AddProxyResponse], error) {
	resp, err := h.endpoints.AddProxyEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.AddProxyResponse)), nil
}

func (h *managementHandler) RemoveProxy(
	ctx context.Context,
	req *connect.Request[endpoints.RemoveProxyRequest],
) (*connect.Response[endpoints.RemoveProxyResponse], error) {
	resp, err := h.endpoints.RemoveProxyEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.RemoveProxyResponse)), nil
}

func (h *managementHandler) UpdateProxy(
	ctx context.Context,
	req *connect.Request[endpoints.UpdateProxyRequest],
) (*connect.Response[endpoints.UpdateProxyResponse], error) {
	resp, err := h.endpoints.UpdateProxyEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.UpdateProxyResponse)), nil
}

func (h *managementHandler) GetStatus(
	ctx context.Context,
	req *connect.Request[endpoints.GetStatusRequest],
) (*connect.Response[endpoints.GetStatusResponse], error) {
	resp, err := h.endpoints.GetStatusEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetStatusResponse)), nil
}

func (h *managementHandler) AddBackend(
	ctx context.Context,
	req *connect.Request[endpoints.AddBackendRequest],
) (*connect.Response[endpoints.AddBackendResponse], error) {
	resp, err := h.endpoints.AddBackendEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.AddBackendResponse)), nil
}

func (h *managementHandler) RemoveBackend(
	ctx context.Context,
	req *connect.Request[endpoints.RemoveBackendRequest],
) (*connect.Response[endpoints.RemoveBackendResponse], error) {
	resp, err := h.endpoints.RemoveBackendEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.RemoveBackendResponse)), nil
}

func (h *managementHandler) UpdateBackend(
	ctx context.Context,
	req *connect.Request[endpoints.UpdateBackendRequest],
) (*connect.Response[endpoints.UpdateBackendResponse], error) {
	resp, err := h.endpoints.UpdateBackendEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.UpdateBackendResponse)), nil
}

func (h *managementHandler) ValidateBackend(
	ctx context.Context,
	req *connect.Request[endpoints.ValidateBackendRequest],
) (*connect.Response[endpoints.ValidateBackendResponse], error) {
	resp, err := h.endpoints.ValidateBackendEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.ValidateBackendResponse)), nil
}

func (h *managementHandler) ProvisionReplica(
	ctx context.Context,
	req *connect.Request[endpoints.ProvisionReplicaRequest],
	stream *connect.ServerStream[endpoints.ProvisionProgress],
) error {
	progress := make(chan *endpoints.ProvisionProgress)
	errChan := make(chan error, 1)

	go func() {
		_, err := h.endpoints.ProvisionReplicaEndpoint(ctx, management.ProvisionReplicaRequest{
			Req:      req.Msg,
			Progress: progress,
		})
		errChan <- err
	}()

	for p := range progress {
		if err := stream.Send(p); err != nil {
			return err
		}
	}

	return <-errChan
}

func (h *managementHandler) StreamLogs(
	ctx context.Context,
	req *connect.Request[endpoints.StreamLogsRequest],
	stream *connect.ServerStream[domain.LogEntry],
) error {
	logs := make(chan *domain.LogEntry)
	errChan := make(chan error, 1)

	go func() {
		_, err := h.endpoints.StreamLogsEndpoint(ctx, management.StreamLogsRequest{
			Req:  req.Msg,
			Logs: logs,
		})
		errChan <- err
	}()

	for entry := range logs {
		if err := stream.Send(entry); err != nil {
			return err
		}
	}

	return <-errChan
}

func (h *managementHandler) InitializeNode(
	ctx context.Context,
	req *connect.Request[endpoints.InitializeNodeRequest],
	stream *connect.ServerStream[endpoints.InitializeNodeProgress],
) error {
	progress := make(chan *endpoints.InitializeNodeProgress)
	errChan := make(chan error, 1)

	go func() {
		_, err := h.endpoints.InitializeNodeEndpoint(ctx, management.InitializeNodeRequest{
			Req:      req.Msg,
			Progress: progress,
		})
		errChan <- err
	}()

	for p := range progress {
		if err := stream.Send(p); err != nil {
			return err
		}
	}

	return <-errChan
}

func (h *managementHandler) InstallNode(
	ctx context.Context,
	req *connect.Request[endpoints.InstallNodeRequest],
	stream *connect.ServerStream[endpoints.InstallNodeProgress],
) error {
	progress := make(chan *endpoints.InstallNodeProgress)
	errChan := make(chan error, 1)

	go func() {
		_, err := h.endpoints.InstallNodeEndpoint(ctx, management.InstallNodeRequest{
			Req:      req.Msg,
			Progress: progress,
		})
		errChan <- err
	}()

	for p := range progress {
		if err := stream.Send(p); err != nil {
			return err
		}
	}

	return <-errChan
}

func (h *managementHandler) BackupBackend(
	ctx context.Context,
	req *connect.Request[endpoints.BackupBackendRequest],
	stream *connect.ServerStream[endpoints.BackupBackendProgress],
) error {
	progress := make(chan *endpoints.BackupBackendProgress)
	errChan := make(chan error, 1)

	go func() {
		_, err := h.endpoints.BackupBackendEndpoint(ctx, management.BackupBackendRequest{
			Req:      req.Msg,
			Progress: progress,
		})
		errChan <- err
	}()

	for p := range progress {
		if err := stream.Send(p); err != nil {
			return err
		}
	}

	return <-errChan
}

func (h *managementHandler) RestoreBackend(
	ctx context.Context,
	req *connect.Request[endpoints.RestoreBackendRequest],
	stream *connect.ServerStream[endpoints.RestoreBackendProgress],
) error {
	progress := make(chan *endpoints.RestoreBackendProgress)
	errChan := make(chan error, 1)

	go func() {
		_, err := h.endpoints.RestoreBackendEndpoint(ctx, management.RestoreBackendRequest{
			Req:      req.Msg,
			Progress: progress,
		})
		errChan <- err
	}()

	for p := range progress {
		if err := stream.Send(p); err != nil {
			return err
		}
	}

	return <-errChan
}

func (h *managementHandler) PromoteBackend(
	ctx context.Context,
	req *connect.Request[endpoints.PromoteBackendRequest],
) (*connect.Response[endpoints.PromoteBackendResponse], error) {
	resp, err := h.endpoints.PromoteBackendEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.PromoteBackendResponse)), nil
}

func (h *managementHandler) SetClusterConfig(
	ctx context.Context,
	req *connect.Request[endpoints.SetClusterConfigRequest],
) (*connect.Response[endpoints.SetClusterConfigResponse], error) {
	resp, err := h.endpoints.SetClusterConfigEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.SetClusterConfigResponse)), nil
}

func (h *managementHandler) GetClusterConfig(
	ctx context.Context,
	req *connect.Request[endpoints.GetClusterConfigRequest],
) (*connect.Response[endpoints.GetClusterConfigResponse], error) {
	resp, err := h.endpoints.GetClusterConfigEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetClusterConfigResponse)), nil
}

func (h *managementHandler) DiscoverCluster(
	ctx context.Context,
	req *connect.Request[endpoints.DiscoverClusterRequest],
) (*connect.Response[endpoints.DiscoverClusterResponse], error) {
	resp, err := h.endpoints.DiscoverClusterEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.DiscoverClusterResponse)), nil
}

func (h *managementHandler) VacuumBackend(
	ctx context.Context,
	req *connect.Request[endpoints.VacuumBackendRequest],
	stream *connect.ServerStream[endpoints.VacuumBackendProgress],
) error {
	progress := make(chan *endpoints.VacuumBackendProgress)
	go func() {
		defer close(progress)
		_, err := h.endpoints.VacuumBackendEndpoint(ctx, &management.VacuumBackendRequest{
			Req:      req.Msg,
			Progress: progress,
		})
		if err != nil {
			slog.Error("Vacuum backend failed", "error", err)
		}
	}()

	for p := range progress {
		if err := stream.Send(p); err != nil {
			return err
		}
	}
	return nil
}

func (h *managementHandler) ExplainQuery(
	ctx context.Context,
	req *connect.Request[endpoints.ExplainQueryRequest],
) (*connect.Response[endpoints.ExplainQueryResponse], error) {
	resp, err := h.endpoints.ExplainQueryEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.ExplainQueryResponse)), nil
}

func (h *managementHandler) GetLogs(
	ctx context.Context,
	req *connect.Request[endpoints.GetLogsRequest],
) (*connect.Response[endpoints.GetLogsResponse], error) {
	resp, err := h.endpoints.GetLogsEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetLogsResponse)), nil
}

func (h *managementHandler) GetMetricsHistory(
	ctx context.Context,
	req *connect.Request[endpoints.GetMetricsHistoryRequest],
) (*connect.Response[endpoints.GetMetricsHistoryResponse], error) {
	resp, err := h.endpoints.GetMetricsHistoryEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetMetricsHistoryResponse)), nil
}

func (h *managementHandler) GetTopQueriesHistory(
	ctx context.Context,
	req *connect.Request[endpoints.GetTopQueriesHistoryRequest],
) (*connect.Response[endpoints.GetTopQueriesHistoryResponse], error) {
	resp, err := h.endpoints.GetTopQueriesHistoryEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetTopQueriesHistoryResponse)), nil
}

func (h *managementHandler) TuneDatabase(
	ctx context.Context,
	req *connect.Request[endpoints.TuneDatabaseRequest],
) (*connect.Response[endpoints.TuneDatabaseResponse], error) {
	resp, err := h.endpoints.TuneDatabaseEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.TuneDatabaseResponse)), nil
}

func (h *managementHandler) ApplyTuning(
	ctx context.Context,
	req *connect.Request[endpoints.ApplyTuningRequest],
) (*connect.Response[endpoints.ApplyTuningResponse], error) {
	resp, err := h.endpoints.ApplyTuningEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.ApplyTuningResponse)), nil
}

func (h *managementHandler) GetAgentInfo(
	ctx context.Context,
	req *connect.Request[endpoints.GetAgentInfoRequest],
) (*connect.Response[endpoints.GetAgentInfoResponse], error) {
	resp, err := h.endpoints.GetAgentInfoEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetAgentInfoResponse)), nil
}

func (h *managementHandler) GetAvailableVersions(
	ctx context.Context,
	req *connect.Request[endpoints.GetAvailableVersionsRequest],
) (*connect.Response[endpoints.GetAvailableVersionsResponse], error) {
	resp, err := h.endpoints.GetAvailableVersionsEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetAvailableVersionsResponse)), nil
}

func (h *managementHandler) Login(
	ctx context.Context,
	req *connect.Request[endpoints.LoginRequest],
) (*connect.Response[endpoints.LoginResponse], error) {
	resp, err := h.endpoints.LoginEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.LoginResponse)), nil
}

func (h *managementHandler) CreateUser(
	ctx context.Context,
	req *connect.Request[endpoints.CreateUserRequest],
) (*connect.Response[endpoints.CreateUserResponse], error) {
	resp, err := h.endpoints.CreateUserEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.CreateUserResponse)), nil
}

func (h *managementHandler) GetServerInfo(
	ctx context.Context,
	req *connect.Request[endpoints.GetServerInfoRequest],
) (*connect.Response[endpoints.GetServerInfoResponse], error) {
	resp, err := h.endpoints.GetServerInfoEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetServerInfoResponse)), nil
}

func (h *managementHandler) GetPostgresInsights(
	ctx context.Context,
	req *connect.Request[endpoints.GetBackendPostgresInsightsRequest],
) (*connect.Response[endpoints.GetBackendPostgresInsightsResponse], error) {
	resp, err := h.endpoints.GetPostgresInsightsEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.GetBackendPostgresInsightsResponse)), nil
}

func (h *managementHandler) RestartBackendService(
	ctx context.Context,
	req *connect.Request[endpoints.RestartBackendServiceRequest],
) (*connect.Response[endpoints.RestartBackendServiceResponse], error) {
	resp, err := h.endpoints.RestartBackendServiceEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.RestartBackendServiceResponse)), nil
}

func (h *managementHandler) ShutdownBackend(
	ctx context.Context,
	req *connect.Request[endpoints.ShutdownBackendRequest],
) (*connect.Response[endpoints.ShutdownBackendResponse], error) {
	resp, err := h.endpoints.ShutdownBackendEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.ShutdownBackendResponse)), nil
}

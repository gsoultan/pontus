package transport

import (
	"context"
	"crypto/subtle"

	kitendpoint "github.com/go-kit/kit/endpoint"
	grpctransport "github.com/go-kit/kit/transport/grpc"
	"github.com/gsoultan/pontus/agent/endpoint"
	"github.com/gsoultan/pontus/agent/services"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type grpcServer struct {
	service.UnimplementedAgentServiceServer
	getSystemInfo       grpctransport.Handler
	updateConfig        grpctransport.Handler
	restartService      grpctransport.Handler
	scheduleMaintenance grpctransport.Handler
	promoteNode         grpctransport.Handler
	executeCommand      kitendpoint.Endpoint
	streamLogs          kitendpoint.Endpoint
	setupReplication    kitendpoint.Endpoint
	initializeDatabase  kitendpoint.Endpoint
	installDatabase     kitendpoint.Endpoint
	backupDatabase      kitendpoint.Endpoint
	restoreDatabase     kitendpoint.Endpoint
	shutdownDatabase    grpctransport.Handler
	removeDatabase      grpctransport.Handler
	vacuumDatabase      kitendpoint.Endpoint
	getPostgresInsights grpctransport.Handler
}

// NewGRPCServer makes a set of endpoints available as a gRPC AgentServiceServer.
func NewGRPCServer(endpoints endpoint.Endpoints, s services.Service) service.AgentServiceServer {
	return &grpcServer{
		getSystemInfo: grpctransport.NewServer(
			endpoints.GetSystemInfoEndpoint,
			decodeGRPCGetSystemInfoRequest,
			encodeGRPCGetSystemInfoResponse,
		),
		updateConfig: grpctransport.NewServer(
			endpoints.UpdateConfigEndpoint,
			decodeGRPCUpdateConfigRequest,
			encodeGRPCUpdateConfigResponse,
		),
		restartService: grpctransport.NewServer(
			endpoints.RestartServiceEndpoint,
			decodeGRPCRestartServiceRequest,
			encodeGRPCRestartServiceResponse,
		),
		scheduleMaintenance: grpctransport.NewServer(
			endpoints.ScheduleMaintenanceEndpoint,
			decodeGRPCScheduleMaintenanceRequest,
			encodeGRPCScheduleMaintenanceResponse,
		),
		promoteNode: grpctransport.NewServer(
			endpoints.PromoteNodeEndpoint,
			decodeGRPCPromoteNodeRequest,
			encodeGRPCPromoteNodeResponse,
		),
		executeCommand:     endpoints.ExecuteCommandEndpoint,
		streamLogs:         endpoints.StreamLogsEndpoint,
		setupReplication:   endpoints.SetupReplicationEndpoint,
		initializeDatabase: endpoints.InitializeDatabaseEndpoint,
		installDatabase:    endpoints.InstallDatabaseEndpoint,
		backupDatabase:     endpoints.BackupDatabaseEndpoint,
		restoreDatabase:    endpoints.RestoreDatabaseEndpoint,
		shutdownDatabase: grpctransport.NewServer(
			endpoints.ShutdownDatabaseEndpoint,
			decodeGRPCShutdownDatabaseRequest,
			encodeGRPCShutdownDatabaseResponse,
		),
		removeDatabase: grpctransport.NewServer(
			endpoints.RemoveDatabaseEndpoint,
			decodeGRPCRemoveDatabaseRequest,
			encodeGRPCRemoveDatabaseResponse,
		),
		vacuumDatabase: endpoints.VacuumDatabaseEndpoint,
		getPostgresInsights: grpctransport.NewServer(
			endpoints.GetPostgresInsightsEndpoint,
			decodeGRPCGetPostgresInsightsRequest,
			encodeGRPCGetPostgresInsightsResponse,
		),
	}
}

func (s *grpcServer) GetSystemInfo(ctx context.Context, req *endpoints.GetSystemInfoRequest) (*endpoints.GetSystemInfoResponse, error) {
	_, resp, err := s.getSystemInfo.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.GetSystemInfoResponse), nil
}

func (s *grpcServer) UpdateConfig(ctx context.Context, req *endpoints.UpdateConfigRequest) (*endpoints.UpdateConfigResponse, error) {
	_, resp, err := s.updateConfig.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.UpdateConfigResponse), nil
}

func (s *grpcServer) RestartService(ctx context.Context, req *endpoints.RestartServiceRequest) (*endpoints.RestartServiceResponse, error) {
	_, resp, err := s.restartService.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.RestartServiceResponse), nil
}

func (s *grpcServer) ScheduleMaintenance(ctx context.Context, req *endpoints.ScheduleMaintenanceRequest) (*endpoints.ScheduleMaintenanceResponse, error) {
	_, resp, err := s.scheduleMaintenance.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.ScheduleMaintenanceResponse), nil
}

func (s *grpcServer) PromoteNode(ctx context.Context, req *endpoints.PromoteNodeRequest) (*endpoints.PromoteNodeResponse, error) {
	_, resp, err := s.promoteNode.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.PromoteNodeResponse), nil
}

func (s *grpcServer) ShutdownDatabase(ctx context.Context, req *endpoints.ShutdownDatabaseRequest) (*endpoints.ShutdownDatabaseResponse, error) {
	_, resp, err := s.shutdownDatabase.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.ShutdownDatabaseResponse), nil
}

func (s *grpcServer) RemoveDatabase(ctx context.Context, req *endpoints.RemoveDatabaseRequest) (*endpoints.RemoveDatabaseResponse, error) {
	_, resp, err := s.removeDatabase.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.RemoveDatabaseResponse), nil
}

func (s *grpcServer) GetPostgresInsights(ctx context.Context, req *endpoints.GetPostgresInsightsRequest) (*endpoints.GetPostgresInsightsResponse, error) {
	_, resp, err := s.getPostgresInsights.ServeGRPC(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.(*endpoints.GetPostgresInsightsResponse), nil
}

func (s *grpcServer) VacuumDatabase(req *endpoints.VacuumDatabaseRequest, stream service.AgentService_VacuumDatabaseServer) error {
	resp, err := s.vacuumDatabase(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.VacuumProgress)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

// authorize reports whether the call carried the expected token.
//
// The comparison is constant-time: a byte-wise `!=` returns on the first
// differing byte, which leaks the length of the matching prefix to anyone who
// can time the response. That is enough to recover a token one byte at a time,
// and this token guards InstallDatabase and PromoteNode.
func authorize(ctx context.Context, token string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "metadata is not provided")
	}

	values := md["authorization"]
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "authorization token is not provided")
	}

	if subtle.ConstantTimeCompare([]byte(values[0]), []byte(token)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid authorization token")
	}

	return nil
}

// TokenInterceptor returns a gRPC unary interceptor that validates the token.
func TokenInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authorize(ctx, token); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamTokenInterceptor returns a gRPC stream interceptor that validates the token.
func StreamTokenInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorize(ss.Context(), token); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// Streaming methods bypass Go Kit endpoints as they are not well-supported in Endpoint abstraction

func (s *grpcServer) ExecuteCommand(req *endpoints.ExecuteCommandRequest, stream service.AgentService_ExecuteCommandServer) error {
	resp, err := s.executeCommand(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.ExecuteCommandResponse)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *grpcServer) StreamLogs(req *endpoints.LogStreamRequest, stream service.AgentService_StreamLogsServer) error {
	resp, err := s.streamLogs(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.LogStreamResponse)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *grpcServer) SetupReplication(req *endpoints.SetupReplicationRequest, stream service.AgentService_SetupReplicationServer) error {
	resp, err := s.setupReplication(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.ReplicationProgress)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *grpcServer) InitializeDatabase(req *endpoints.InitializeDatabaseRequest, stream service.AgentService_InitializeDatabaseServer) error {
	resp, err := s.initializeDatabase(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.InitializeProgress)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *grpcServer) InstallDatabase(req *endpoints.InstallDatabaseRequest, stream service.AgentService_InstallDatabaseServer) error {
	resp, err := s.installDatabase(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.InstallProgress)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *grpcServer) BackupDatabase(req *endpoints.BackupDatabaseRequest, stream service.AgentService_BackupDatabaseServer) error {
	resp, err := s.backupDatabase(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.BackupProgress)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *grpcServer) RestoreDatabase(req *endpoints.RestoreDatabaseRequest, stream service.AgentService_RestoreDatabaseServer) error {
	resp, err := s.restoreDatabase(stream.Context(), req)
	if err != nil {
		return err
	}
	out := resp.(<-chan *endpoints.RestoreProgress)
	for msg := range out {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

// Decoders and Encoders

func decodeGRPCGetSystemInfoRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.GetSystemInfoRequest), nil
}

func encodeGRPCGetSystemInfoResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.GetSystemInfoResponse), nil
}

func decodeGRPCUpdateConfigRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.UpdateConfigRequest), nil
}

func encodeGRPCUpdateConfigResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.UpdateConfigResponse), nil
}

func decodeGRPCRestartServiceRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.RestartServiceRequest), nil
}

func encodeGRPCRestartServiceResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.RestartServiceResponse), nil
}

func decodeGRPCScheduleMaintenanceRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.ScheduleMaintenanceRequest), nil
}

func encodeGRPCScheduleMaintenanceResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.ScheduleMaintenanceResponse), nil
}

func decodeGRPCPromoteNodeRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.PromoteNodeRequest), nil
}

func encodeGRPCPromoteNodeResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.PromoteNodeResponse), nil
}

func decodeGRPCShutdownDatabaseRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.ShutdownDatabaseRequest), nil
}

func encodeGRPCShutdownDatabaseResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.ShutdownDatabaseResponse), nil
}

func decodeGRPCRemoveDatabaseRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.RemoveDatabaseRequest), nil
}

func encodeGRPCRemoveDatabaseResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.RemoveDatabaseResponse), nil
}

func decodeGRPCVacuumDatabaseRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.VacuumDatabaseRequest), nil
}

func encodeGRPCVacuumDatabaseResponse(_ context.Context, response any) (any, error) {
	return response, nil
}

func decodeGRPCGetPostgresInsightsRequest(_ context.Context, grpcReq any) (any, error) {
	return grpcReq.(*endpoints.GetPostgresInsightsRequest), nil
}

func encodeGRPCGetPostgresInsightsResponse(_ context.Context, response any) (any, error) {
	return response.(*endpoints.GetPostgresInsightsResponse), nil
}

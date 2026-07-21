package endpoint

import (
	"context"

	"github.com/go-kit/kit/endpoint"
	"github.com/gsoultan/pontus/agent/services"
	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Endpoints collects all the endpoints that compose an agent service.
type Endpoints struct {
	GetSystemInfoEndpoint       endpoint.Endpoint
	UpdateConfigEndpoint        endpoint.Endpoint
	RestartServiceEndpoint      endpoint.Endpoint
	ScheduleMaintenanceEndpoint endpoint.Endpoint
	PromoteNodeEndpoint         endpoint.Endpoint
	ExecuteCommandEndpoint      endpoint.Endpoint
	StreamLogsEndpoint          endpoint.Endpoint
	SetupReplicationEndpoint    endpoint.Endpoint
	InitializeDatabaseEndpoint  endpoint.Endpoint
	InstallDatabaseEndpoint     endpoint.Endpoint
	BackupDatabaseEndpoint      endpoint.Endpoint
	RestoreDatabaseEndpoint     endpoint.Endpoint
	ShutdownDatabaseEndpoint    endpoint.Endpoint
	RemoveDatabaseEndpoint      endpoint.Endpoint
	VacuumDatabaseEndpoint      endpoint.Endpoint
	GetPostgresInsightsEndpoint endpoint.Endpoint
}

// MakeEndpoints returns an Endpoints struct where each endpoint invokes
// the corresponding method on the provided service.
func MakeEndpoints(s services.Service) Endpoints {
	return Endpoints{
		GetSystemInfoEndpoint:       makeGetSystemInfoEndpoint(s),
		UpdateConfigEndpoint:        makeUpdateConfigEndpoint(s),
		RestartServiceEndpoint:      makeRestartServiceEndpoint(s),
		ScheduleMaintenanceEndpoint: makeScheduleMaintenanceEndpoint(s),
		PromoteNodeEndpoint:         makePromoteNodeEndpoint(s),
		ExecuteCommandEndpoint:      makeExecuteCommandEndpoint(s),
		StreamLogsEndpoint:          makeStreamLogsEndpoint(s),
		SetupReplicationEndpoint:    makeSetupReplicationEndpoint(s),
		InitializeDatabaseEndpoint:  makeInitializeDatabaseEndpoint(s),
		InstallDatabaseEndpoint:     makeInstallDatabaseEndpoint(s),
		BackupDatabaseEndpoint:      makeBackupDatabaseEndpoint(s),
		RestoreDatabaseEndpoint:     makeRestoreDatabaseEndpoint(s),
		ShutdownDatabaseEndpoint:    makeShutdownDatabaseEndpoint(s),
		RemoveDatabaseEndpoint:      makeRemoveDatabaseEndpoint(s),
		VacuumDatabaseEndpoint:      makeVacuumDatabaseEndpoint(s),
		GetPostgresInsightsEndpoint: makeGetPostgresInsightsEndpoint(s),
	}
}

func makeGetSystemInfoEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		return s.GetSystemInfo(ctx)
	}
}

func makeUpdateConfigEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.UpdateConfigRequest)
		return s.UpdateConfig(ctx, req)
	}
}

func makeRestartServiceEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.RestartServiceRequest)
		return s.RestartService(ctx, req)
	}
}

func makeScheduleMaintenanceEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.ScheduleMaintenanceRequest)
		return s.ScheduleMaintenance(ctx, req)
	}
}

func makePromoteNodeEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.PromoteNodeRequest)
		return s.PromoteNode(ctx, req)
	}
}

func makeExecuteCommandEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.ExecuteCommandRequest)
		return s.ExecuteCommand(ctx, req)
	}
}

func makeStreamLogsEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.LogStreamRequest)
		return s.StreamLogs(ctx, req)
	}
}

func makeSetupReplicationEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.SetupReplicationRequest)
		return s.SetupReplication(ctx, req)
	}
}

func makeInitializeDatabaseEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.InitializeDatabaseRequest)
		return s.InitializeDatabase(ctx, req)
	}
}

func makeInstallDatabaseEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.InstallDatabaseRequest)
		return s.InstallDatabase(ctx, req)
	}
}

func makeBackupDatabaseEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.BackupDatabaseRequest)
		return s.BackupDatabase(ctx, req)
	}
}

func makeRestoreDatabaseEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.RestoreDatabaseRequest)
		return s.RestoreDatabase(ctx, req)
	}
}

func makeShutdownDatabaseEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.ShutdownDatabaseRequest)
		return s.ShutdownDatabase(ctx, req)
	}
}

func makeRemoveDatabaseEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.RemoveDatabaseRequest)
		return s.RemoveDatabase(ctx, req)
	}
}

func makeVacuumDatabaseEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.VacuumDatabaseRequest)
		return s.VacuumDatabase(ctx, req)
	}
}

func makeGetPostgresInsightsEndpoint(s services.Service) endpoint.Endpoint {
	return func(ctx context.Context, request any) (any, error) {
		req := request.(*endpoints.GetPostgresInsightsRequest)
		return s.GetPostgresInsights(ctx, req)
	}
}

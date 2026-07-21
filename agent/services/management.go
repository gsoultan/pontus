package services

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Management defines the interface for host management operations.
type Management interface {
	// UpdateConfig updates a configuration file on the host.
	UpdateConfig(ctx context.Context, req *endpoints.UpdateConfigRequest) (*endpoints.UpdateConfigResponse, error)

	// ExecuteCommand executes a command and returns a channel for streaming output.
	ExecuteCommand(ctx context.Context, req *endpoints.ExecuteCommandRequest) (<-chan *endpoints.ExecuteCommandResponse, error)

	// RestartService restarts a system service.
	RestartService(ctx context.Context, req *endpoints.RestartServiceRequest) (*endpoints.RestartServiceResponse, error)

	// ScheduleMaintenance schedules a maintenance task.
	ScheduleMaintenance(ctx context.Context, req *endpoints.ScheduleMaintenanceRequest) (*endpoints.ScheduleMaintenanceResponse, error)

	// SetupReplication configures the host as a replica.
	SetupReplication(ctx context.Context, req *endpoints.SetupReplicationRequest) (<-chan *endpoints.ReplicationProgress, error)

	// PromoteNode promotes a replica to primary.
	PromoteNode(ctx context.Context, req *endpoints.PromoteNodeRequest) (*endpoints.PromoteNodeResponse, error)

	// InitializeDatabase initializes a new database instance.
	InitializeDatabase(ctx context.Context, req *endpoints.InitializeDatabaseRequest) (<-chan *endpoints.InitializeProgress, error)

	// InstallDatabase installs the database software.
	InstallDatabase(ctx context.Context, req *endpoints.InstallDatabaseRequest) (<-chan *endpoints.InstallProgress, error)

	// BackupDatabase creates a backup of the database.
	BackupDatabase(ctx context.Context, req *endpoints.BackupDatabaseRequest) (<-chan *endpoints.BackupProgress, error)

	// RestoreDatabase restores the database from a backup.
	RestoreDatabase(ctx context.Context, req *endpoints.RestoreDatabaseRequest) (<-chan *endpoints.RestoreProgress, error)

	// ShutdownDatabase stops the database service.
	ShutdownDatabase(ctx context.Context, req *endpoints.ShutdownDatabaseRequest) (*endpoints.ShutdownDatabaseResponse, error)

	// RemoveDatabase stops the database and removes its data directory.
	RemoveDatabase(ctx context.Context, req *endpoints.RemoveDatabaseRequest) (*endpoints.RemoveDatabaseResponse, error)

	// VacuumDatabase performs a VACUUM operation on the database.
	VacuumDatabase(ctx context.Context, req *endpoints.VacuumDatabaseRequest) (<-chan *endpoints.VacuumProgress, error)
}

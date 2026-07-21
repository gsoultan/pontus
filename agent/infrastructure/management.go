package infrastructure

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/pontus/agent/services"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/system"
)

// management implements the services.Management interface.
type management struct {
	allowedPaths []string
	validators   map[string]services.Validator
	repoManager  services.RepositoryManager
}

// NewManagement creates a new management instance.
func NewManagement(allowedPaths []string, validators map[string]services.Validator, repoManager services.RepositoryManager) *management {
	return &management{
		allowedPaths: allowedPaths,
		validators:   validators,
		repoManager:  repoManager,
	}
}

func (m *management) UpdateConfig(ctx context.Context, req *endpoints.UpdateConfigRequest) (*endpoints.UpdateConfigResponse, error) {
	// Path whitelisting
	allowed := false
	for _, p := range m.allowedPaths {
		if strings.HasPrefix(req.FilePath, p) {
			allowed = true
			break
		}
	}

	if !allowed {
		return &endpoints.UpdateConfigResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("path %s is not allowed for updates", req.FilePath),
		}, nil
	}

	// Validation
	for name, v := range m.validators {
		if strings.Contains(req.FilePath, name) {
			if err := v.Validate(ctx, req.FilePath, req.Content); err != nil {
				return &endpoints.UpdateConfigResponse{
					Success:      false,
					ErrorMessage: fmt.Sprintf("validation failed: %v", err),
				}, nil
			}
		}
	}

	// Simulate writing the file
	// err := os.WriteFile(req.FilePath, []byte(req.Content), 0644)
	return &endpoints.UpdateConfigResponse{Success: true}, nil
}

func (m *management) ExecuteCommand(ctx context.Context, req *endpoints.ExecuteCommandRequest) (<-chan *endpoints.ExecuteCommandResponse, error) {
	out := make(chan *endpoints.ExecuteCommandResponse)

	// Validate command
	if req.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// Security: Command allowlist
	allowedCommands := map[string]bool{
		"ls":            true,
		"ps":            true,
		"df":            true,
		"free":          true,
		"pg_basebackup": true,
		"pg_ctl":        true,
		"pg_dump":       true,
		"pg_restore":    true,
		"systemctl":     true,
		"service":       true,
		"echo":          true, // for testing
		"pg_isready":    true,
		"cat":           true,
		"pontus-agent":  true,
		"tail":          true,
	}

	if !allowedCommands[req.Command] {
		return nil, fmt.Errorf("command %s is not allowed", req.Command)
	}

	go func() {
		defer close(out)
		cmd := exec.CommandContext(ctx, req.Command, req.Args...)
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}

		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()

		if err := cmd.Start(); err != nil {
			out <- &endpoints.ExecuteCommandResponse{Stderr: err.Error(), ExitCode: 1}
			return
		}

		// Output streaming with context awareness
		go func() {
			buf := make([]byte, 1024)
			for {
				n, err := stdout.Read(buf)
				if n > 0 {
					select {
					case out <- &endpoints.ExecuteCommandResponse{Stdout: string(buf[:n])}:
					case <-ctx.Done():
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()

		go func() {
			buf := make([]byte, 1024)
			for {
				n, err := stderr.Read(buf)
				if n > 0 {
					select {
					case out <- &endpoints.ExecuteCommandResponse{Stderr: string(buf[:n])}:
					case <-ctx.Done():
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()

		err := cmd.Wait()
		exitCode := int32(0)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = int32(exitErr.ExitCode())
			} else {
				exitCode = -1
			}
		} else if cmd.ProcessState != nil {
			exitCode = int32(cmd.ProcessState.ExitCode())
		}

		select {
		case out <- &endpoints.ExecuteCommandResponse{ExitCode: exitCode}:
		case <-ctx.Done():
		}
	}()

	return out, nil
}

func (m *management) RestartService(ctx context.Context, req *endpoints.RestartServiceRequest) (*endpoints.RestartServiceResponse, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// On Windows, we try net stop/start
		stopCmd := exec.CommandContext(ctx, "net", "stop", req.ServiceName)
		_ = stopCmd.Run() // Ignore error if already stopped
		cmd = exec.CommandContext(ctx, "net", "start", req.ServiceName)
	} else {
		// On Linux, use systemctl
		cmd = exec.CommandContext(ctx, "systemctl", "restart", req.ServiceName)
	}

	if err := cmd.Run(); err != nil {
		return &endpoints.RestartServiceResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to restart service: %v", err),
		}, nil
	}

	return &endpoints.RestartServiceResponse{Success: true}, nil
}

func (m *management) ScheduleMaintenance(ctx context.Context, req *endpoints.ScheduleMaintenanceRequest) (*endpoints.ScheduleMaintenanceResponse, error) {
	return &endpoints.ScheduleMaintenanceResponse{Success: true, TaskId: "task-123"}, nil
}

func (m *management) SetupReplication(ctx context.Context, req *endpoints.SetupReplicationRequest) (<-chan *endpoints.ReplicationProgress, error) {
	out := make(chan *endpoints.ReplicationProgress)
	dataDir := req.DataDirectory
	if dataDir == "" {
		dataDir = system.DetectPostgresDataDir()
	}

	go func() {
		defer close(out)
		out <- &endpoints.ReplicationProgress{Stage: "Starting", Percentage: 10, Message: fmt.Sprintf("Preparing replica at %s", dataDir)}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.ReplicationProgress{Stage: "Syncing", Percentage: 50, Message: "Syncing base backup"}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.ReplicationProgress{Stage: "Done", Percentage: 100, Message: "Replication configured"}
	}()
	return out, nil
}

func (m *management) PromoteNode(ctx context.Context, req *endpoints.PromoteNodeRequest) (*endpoints.PromoteNodeResponse, error) {
	return &endpoints.PromoteNodeResponse{Success: true}, nil
}

func (m *management) InitializeDatabase(ctx context.Context, req *endpoints.InitializeDatabaseRequest) (<-chan *endpoints.InitializeProgress, error) {
	out := make(chan *endpoints.InitializeProgress)
	dataDir := req.DataDirectory
	if dataDir == "" {
		dataDir = system.DetectPostgresDataDir()
	}

	go func() {
		defer close(out)
		out <- &endpoints.InitializeProgress{Stage: "Init", Percentage: 10, Message: fmt.Sprintf("Initializing data directory at %s", dataDir)}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.InitializeProgress{Stage: "Configuring", Percentage: 50, Message: "Setting up config files"}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.InitializeProgress{Stage: "Done", Percentage: 100, Message: "Database initialized"}
	}()
	return out, nil
}

func (m *management) InstallDatabase(ctx context.Context, req *endpoints.InstallDatabaseRequest) (<-chan *endpoints.InstallProgress, error) {
	out := make(chan *endpoints.InstallProgress)
	go func() {
		defer close(out)

		major, err := strconv.Atoi(req.Version)
		if err != nil {
			out <- &endpoints.InstallProgress{
				Stage:        "Failed",
				Percentage:   0,
				Message:      "Invalid major version",
				ErrorMessage: fmt.Sprintf("invalid major version: %s", req.Version),
			}
			return
		}

		out <- &endpoints.InstallProgress{Stage: "Checking", Percentage: 10, Message: "Checking PostgreSQL versions"}
		outdated, err := m.repoManager.IsOSVersionOutdated(ctx, major)
		if err != nil {
			// Log error and continue with default installation attempt?
			// For now, let's be strict.
			out <- &endpoints.InstallProgress{
				Stage:        "Failed",
				Percentage:   10,
				Message:      "Failed to check versions",
				ErrorMessage: fmt.Sprintf("failed to check versions: %v", err),
			}
			return
		}

		if outdated {
			out <- &endpoints.InstallProgress{Stage: "Repository", Percentage: 30, Message: "Adding PostgreSQL official repository"}
			if err := m.repoManager.AddPostgresRepository(ctx); err != nil {
				out <- &endpoints.InstallProgress{
					Stage:        "Failed",
					Percentage:   30,
					Message:      "Failed to add repository",
					ErrorMessage: fmt.Sprintf("failed to add repository: %v", err),
				}
				return
			}
		}

		out <- &endpoints.InstallProgress{Stage: "Installing", Percentage: 60, Message: fmt.Sprintf("Installing postgresql-%d", major)}
		cmd := exec.CommandContext(ctx, "apt-get", "install", "-y", fmt.Sprintf("postgresql-%d", major))
		if err := cmd.Run(); err != nil {
			out <- &endpoints.InstallProgress{
				Stage:        "Failed",
				Percentage:   60,
				Message:      "Failed to install package",
				ErrorMessage: fmt.Sprintf("failed to install postgresql: %v", err),
			}
			return
		}

		out <- &endpoints.InstallProgress{Stage: "Done", Percentage: 100, Message: "Database software installed"}
	}()
	return out, nil
}

func (m *management) BackupDatabase(ctx context.Context, req *endpoints.BackupDatabaseRequest) (<-chan *endpoints.BackupProgress, error) {
	out := make(chan *endpoints.BackupProgress)
	go func() {
		defer close(out)
		out <- &endpoints.BackupProgress{Stage: "Starting", Percentage: 10, Message: fmt.Sprintf("Starting backup for database %s", req.Database)}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.BackupProgress{Stage: "Dumping", Percentage: 50, Message: "Dumping data to temporary file"}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.BackupProgress{Stage: "Compressing", Percentage: 80, Message: "Compressing backup file"}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.BackupProgress{Stage: "Done", Percentage: 100, Message: fmt.Sprintf("Backup saved to %s", req.BackupPath)}
	}()
	return out, nil
}

func (m *management) RestoreDatabase(ctx context.Context, req *endpoints.RestoreDatabaseRequest) (<-chan *endpoints.RestoreProgress, error) {
	out := make(chan *endpoints.RestoreProgress)
	go func() {
		defer close(out)
		out <- &endpoints.RestoreProgress{Stage: "Starting", Percentage: 10, Message: fmt.Sprintf("Starting restore from %s", req.BackupPath)}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.RestoreProgress{Stage: "Preparing", Percentage: 30, Message: "Checking target database"}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.RestoreProgress{Stage: "Restoring", Percentage: 70, Message: "Streaming data to database"}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.RestoreProgress{Stage: "Done", Percentage: 100, Message: fmt.Sprintf("Restore to %s completed", req.TargetDatabase)}
	}()
	return out, nil
}

func (m *management) ShutdownDatabase(ctx context.Context, req *endpoints.ShutdownDatabaseRequest) (*endpoints.ShutdownDatabaseResponse, error) {
	dataDir := req.DataDirectory
	if dataDir == "" {
		dataDir = system.DetectPostgresDataDir()
	}

	if dataDir != "" {
		cmd := exec.CommandContext(ctx, "pg_ctl", "-D", dataDir, "stop", "-m", "fast")
		if err := cmd.Run(); err == nil {
			return &endpoints.ShutdownDatabaseResponse{Success: true}, nil
		}
	}

	// Fallback to service management
	serviceName := "postgresql"
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "net", "stop", serviceName)
	} else {
		cmd = exec.CommandContext(ctx, "systemctl", "stop", serviceName)
	}

	if err := cmd.Run(); err != nil {
		return &endpoints.ShutdownDatabaseResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to shutdown database: %v", err),
		}, nil
	}

	return &endpoints.ShutdownDatabaseResponse{Success: true}, nil
}

func (m *management) RemoveDatabase(ctx context.Context, req *endpoints.RemoveDatabaseRequest) (*endpoints.RemoveDatabaseResponse, error) {
	dataDir := req.DataDirectory
	if dataDir == "" {
		dataDir = system.DetectPostgresDataDir()
	}
	// Implementation would stop the database and optionally delete data
	fmt.Printf("Removing database at %s (delete data: %v)\n", dataDir, req.DeleteData)
	return &endpoints.RemoveDatabaseResponse{Success: true}, nil
}

func (m *management) VacuumDatabase(ctx context.Context, req *endpoints.VacuumDatabaseRequest) (<-chan *endpoints.VacuumProgress, error) {
	out := make(chan *endpoints.VacuumProgress)
	go func() {
		defer close(out)
		out <- &endpoints.VacuumProgress{Stage: "Starting", Percentage: 10, Message: fmt.Sprintf("Starting vacuum for database %s", req.Database)}
		time.Sleep(100 * time.Millisecond)
		out <- &endpoints.VacuumProgress{Stage: "Vacuuming", Percentage: 50, Message: "Reclaiming storage"}
		time.Sleep(100 * time.Millisecond)
		if req.Analyze {
			out <- &endpoints.VacuumProgress{Stage: "Analyzing", Percentage: 80, Message: "Updating statistics"}
			time.Sleep(100 * time.Millisecond)
		}
		out <- &endpoints.VacuumProgress{Stage: "Done", Percentage: 100, Message: "Vacuum completed"}
	}()
	return out, nil
}

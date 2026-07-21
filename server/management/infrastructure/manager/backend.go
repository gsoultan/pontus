package manager

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"slices"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/internal/orchestration"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
)

// Backend implements BackendService.
type Backend struct {
	registry *registry.Registry
}

func NewBackend(registry *registry.Registry) *Backend {
	return &Backend{registry: registry}
}

func (m *Backend) AddBackend(ctx context.Context, req *endpoints.AddBackendRequest) (*endpoints.AddBackendResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	p.Mu.Lock()
	defer p.Mu.Unlock()

	ps, ok := p.Proxies[req.ProxyId]
	if !ok {
		return nil, fmt.Errorf("proxy not found")
	}

	role := pool2.Role(req.Config.Role)
	if role == "" {
		role = pool2.RolePrimary
	}
	weight := int(req.Config.Weight)
	if weight <= 0 {
		weight = 1
	}

	agentAddr := req.Config.AgentAddress
	if agentAddr == "" {
		// If agent address not provided, we try to infer it from backend address
		if req.Config.Address != "" {
			host, _, _ := net.SplitHostPort(req.Config.Address)
			agentAddr = net.JoinHostPort(host, "9091")
			req.Config.AgentAddress = agentAddr
		} else {
			return nil, fmt.Errorf("agent address is mandatory")
		}
	}

	backendAddr := req.Config.Address
	if backendAddr == "" {
		// If backend address not provided, use agent host and default postgres port
		host, _, err := net.SplitHostPort(agentAddr)
		if err != nil {
			host = agentAddr // Might be just host
		}
		backendAddr = net.JoinHostPort(host, "5432")
		req.Config.Address = backendAddr
	}

	backend, err := pool2.NewServer(backendAddr, req.Config.Zone, agentAddr, req.Config.AgentToken, role, weight, ps.Config.MaxConns, 0, m.registry.DialTimeout(), p.Handler, p.BackendTLS, m.registry.Monitor())
	if err != nil {
		return nil, fmt.Errorf("failed to create backend: %w", err)
	}

	// Install database if requested
	if req.Config.ManagedByAgent && req.Config.AgentConfig != nil && req.Config.AgentConfig.InstallIfMissing {
		installReq := &endpoints.InstallDatabaseRequest{
			Version: req.Config.AgentConfig.Version,
		}
		slog.Info("Installing database via agent", "address", agentAddr, "version", installReq.Version)
		stream, err := backend.AgentClient().InstallDatabase(ctx, installReq)
		if err != nil {
			backend.Close()
			return nil, fmt.Errorf("failed to start database installation: %w", err)
		}

		for {
			progress, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				backend.Close()
				return nil, fmt.Errorf("database installation failed: %w", err)
			}
			slog.Debug("Installation progress", "stage", progress.Stage, "percentage", progress.Percentage, "message", progress.Message)
		}
	}

	// Initialize database if managed by agent
	if req.Config.ManagedByAgent && req.Config.AgentConfig != nil {
		if backend.AgentClient() == nil {
			backend.Close()
			return nil, fmt.Errorf("managed by agent requested but agent is not reachable at %s", agentAddr)
		}

		initReq := &endpoints.InitializeDatabaseRequest{
			DataDirectory:   req.Config.AgentConfig.DataDirectory,
			Version:         req.Config.AgentConfig.Version,
			InitialDatabase: req.Config.AgentConfig.InitialDatabase,
			InitialUser:     req.Config.AgentConfig.InitialUser,
			InitialPassword: req.Config.AgentConfig.InitialPassword,
		}

		slog.Info("Initializing database via agent", "address", agentAddr, "dataDir", initReq.DataDirectory)
		stream, err := backend.AgentClient().InitializeDatabase(ctx, initReq)
		if err != nil {
			backend.Close()
			return nil, fmt.Errorf("failed to start database initialization: %w", err)
		}

		for {
			progress, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				backend.Close()
				return nil, fmt.Errorf("database initialization failed: %w", err)
			}
			slog.Debug("Initialization progress", "stage", progress.Stage, "percentage", progress.Percentage, "message", progress.Message)
		}
	}

	backend.Start(ps.Ctx)

	ps.Backends = append(ps.Backends, backend)
	ps.Config.Backends = append(ps.Config.Backends, req.Config)

	if err := m.registry.UpsertProject(p.Config); err != nil {
		backend.Close()
		ps.Backends = ps.Backends[:len(ps.Backends)-1]
		ps.Config.Backends = ps.Config.Backends[:len(ps.Config.Backends)-1]
		return nil, err
	}

	return &endpoints.AddBackendResponse{}, nil
}

func (m *Backend) RemoveBackend(ctx context.Context, req *endpoints.RemoveBackendRequest) (*endpoints.RemoveBackendResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	p.Mu.Lock()
	defer p.Mu.Unlock()

	ps, ok := p.Proxies[req.ProxyId]
	if !ok {
		return nil, fmt.Errorf("proxy not found")
	}

	idx := slices.IndexFunc(ps.Backends, func(b pool2.Backend) bool {
		return b.Address() == req.Address
	})

	if idx == -1 {
		return nil, fmt.Errorf("backend not found")
	}

	var configToRemove *domain.BackendConfig
	for i, b := range ps.Config.Backends {
		if b.Address == req.Address {
			configToRemove = b
			ps.Config.Backends = append(ps.Config.Backends[:i], ps.Config.Backends[i+1:]...)
			break
		}
	}

	// Shutdown/Remove database if managed by agent
	if configToRemove != nil && configToRemove.ManagedByAgent && ps.Backends[idx].AgentClient() != nil {
		slog.Info("Removing database via agent", "address", configToRemove.Address)
		_, _ = ps.Backends[idx].AgentClient().RemoveDatabase(ctx, &endpoints.RemoveDatabaseRequest{
			DataDirectory: configToRemove.AgentConfig.DataDirectory,
			DeleteData:    false, // Safeguard: don't delete data by default unless we add a flag to RemoveBackendRequest
		})
	}

	ps.Backends[idx].Close()
	ps.Backends = append(ps.Backends[:idx], ps.Backends[idx+1:]...)

	if err := m.registry.UpsertProject(p.Config); err != nil {
		return nil, err
	}

	return &endpoints.RemoveBackendResponse{}, nil
}

func (m *Backend) UpdateBackend(ctx context.Context, req *endpoints.UpdateBackendRequest) (*endpoints.UpdateBackendResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	p.Mu.Lock()
	defer p.Mu.Unlock()

	ps, ok := p.Proxies[req.ProxyId]
	if !ok {
		return nil, fmt.Errorf("proxy not found")
	}

	idx := slices.IndexFunc(ps.Backends, func(b pool2.Backend) bool {
		return b.Address() == req.Address
	})

	if idx == -1 {
		return nil, fmt.Errorf("backend not found")
	}

	backend := ps.Backends[idx]
	backend.SetWeight(int(req.Config.Weight))

	// Update role if changed
	newRole := pool2.Role(req.Config.Role)
	if newRole != "" && newRole != backend.Role() {
		// In a real implementation, we might need more logic here
		// But pool.Server.deepCheck handles role detection.
		// Forcing it here if explicitly set.
	}

	// Update the stored config
	for i, b := range ps.Config.Backends {
		if b.Address == req.Address {
			ps.Config.Backends[i] = req.Config
			break
		}
	}

	if err := m.registry.UpsertProject(p.Config); err != nil {
		return nil, err
	}

	return &endpoints.UpdateBackendResponse{}, nil
}

func (m *Backend) ProvisionReplica(ctx context.Context, req *endpoints.ProvisionReplicaRequest, progress chan<- *endpoints.ProvisionProgress) error {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		close(progress)
		return err
	}

	p.Mu.RLock()
	ps, ok := p.Proxies[req.ProxyId]
	p.Mu.RUnlock()
	if !ok {
		close(progress)
		return fmt.Errorf("proxy not found")
	}

	if ps.FailoverMgr == nil {
		close(progress)
		return fmt.Errorf("failover manager not available for this proxy")
	}

	return ps.FailoverMgr.ProvisionReplica(ctx, req, progress)
}

func (m *Backend) ValidateBackend(ctx context.Context, req *endpoints.ValidateBackendRequest) (*endpoints.ValidateBackendResponse, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", req.Address, 5*time.Second)
	if err != nil {
		return &endpoints.ValidateBackendResponse{Success: false, Message: err.Error()}, nil
	}
	defer conn.Close()

	return &endpoints.ValidateBackendResponse{
		Success:   true,
		Message:   "Connection successful",
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}

func (m *Backend) InitializeNode(ctx context.Context, req *endpoints.InitializeNodeRequest, progress chan<- *endpoints.InitializeNodeProgress) error {
	defer close(progress)

	client, err := orchestration.NewAgentClient(req.HostAddress, req.AgentToken)
	if err != nil {
		progress <- &endpoints.InitializeNodeProgress{Stage: "Failed", Message: "Failed to connect to agent: " + err.Error()}
		return err
	}
	defer client.Close()

	stream, err := client.InitializeDatabase(ctx, &endpoints.InitializeDatabaseRequest{
		DataDirectory: req.DataDirectory,
		Version:       req.Version,
	})
	if err != nil {
		progress <- &endpoints.InitializeNodeProgress{Stage: "Failed", Message: "Failed to start initialization: " + err.Error()}
		return err
	}

	for {
		p, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			progress <- &endpoints.InitializeNodeProgress{Stage: "Failed", Message: "Initialization failed: " + err.Error()}
			return err
		}
		progress <- &endpoints.InitializeNodeProgress{
			Stage:      p.Stage,
			Percentage: p.Percentage,
			Message:    p.Message,
		}
	}

	return nil
}

func (m *Backend) InstallNode(ctx context.Context, req *endpoints.InstallNodeRequest, progress chan<- *endpoints.InstallNodeProgress) error {
	defer close(progress)

	client, err := orchestration.NewAgentClient(req.HostAddress, req.AgentToken)
	if err != nil {
		progress <- &endpoints.InstallNodeProgress{Stage: "Failed", Message: "Failed to connect to agent: " + err.Error()}
		return err
	}
	defer client.Close()

	stream, err := client.InstallDatabase(ctx, &endpoints.InstallDatabaseRequest{
		Version:         req.Version,
		TargetDirectory: req.TargetDirectory,
	})
	if err != nil {
		progress <- &endpoints.InstallNodeProgress{Stage: "Failed", Message: "Failed to start installation: " + err.Error()}
		return err
	}

	for {
		p, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			progress <- &endpoints.InstallNodeProgress{Stage: "Failed", Message: "Installation failed: " + err.Error()}
			return err
		}
		progress <- &endpoints.InstallNodeProgress{
			Stage:      p.Stage,
			Percentage: p.Percentage,
			Message:    p.Message,
		}
	}

	return nil
}

func (m *Backend) BackupBackend(ctx context.Context, req *endpoints.BackupBackendRequest, progress chan<- *endpoints.BackupBackendProgress) error {
	defer close(progress)

	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return err
	}

	p.Mu.RLock()
	ps, ok := p.Proxies[req.ProxyId]
	p.Mu.RUnlock()
	if !ok {
		return fmt.Errorf("proxy not found")
	}

	idx := slices.IndexFunc(ps.Backends, func(b pool2.Backend) bool {
		return b.Address() == req.Address
	})

	if idx == -1 {
		return fmt.Errorf("backend not found")
	}

	backend := ps.Backends[idx]
	agent := backend.AgentClient()
	if agent == nil {
		return fmt.Errorf("agent not connected for this backend")
	}

	stream, err := agent.BackupDatabase(ctx, &endpoints.BackupDatabaseRequest{
		BackupPath: req.BackupPath,
		Database:   "postgres", // Default or extract from config
	})
	if err != nil {
		return err
	}

	for {
		p, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		progress <- &endpoints.BackupBackendProgress{
			Stage:      p.Stage,
			Percentage: p.Percentage,
			Message:    p.Message,
		}
	}

	return nil
}

func (m *Backend) RestoreBackend(ctx context.Context, req *endpoints.RestoreBackendRequest, progress chan<- *endpoints.RestoreBackendProgress) error {
	defer close(progress)

	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return err
	}

	p.Mu.RLock()
	ps, ok := p.Proxies[req.ProxyId]
	p.Mu.RUnlock()
	if !ok {
		return fmt.Errorf("proxy not found")
	}

	idx := slices.IndexFunc(ps.Backends, func(b pool2.Backend) bool {
		return b.Address() == req.Address
	})

	if idx == -1 {
		return fmt.Errorf("backend not found")
	}

	backend := ps.Backends[idx]
	agent := backend.AgentClient()
	if agent == nil {
		return fmt.Errorf("agent not connected for this backend")
	}

	stream, err := agent.RestoreDatabase(ctx, &endpoints.RestoreDatabaseRequest{
		BackupPath:     req.BackupPath,
		TargetDatabase: "postgres",
	})
	if err != nil {
		return err
	}

	for {
		p, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		progress <- &endpoints.RestoreBackendProgress{
			Stage:      p.Stage,
			Percentage: p.Percentage,
			Message:    p.Message,
		}
	}

	return nil
}

func (m *Backend) PromoteBackend(ctx context.Context, req *endpoints.PromoteBackendRequest) (*endpoints.PromoteBackendResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	p.Mu.RLock()
	ps, ok := p.Proxies[req.ProxyId]
	p.Mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("proxy not found")
	}

	idx := slices.IndexFunc(ps.Backends, func(b pool2.Backend) bool {
		return b.Address() == req.Address
	})

	if idx == -1 {
		return nil, fmt.Errorf("backend not found")
	}

	backend := ps.Backends[idx]
	agent := backend.AgentClient()
	if agent == nil {
		return nil, fmt.Errorf("agent not connected")
	}

	resp, err := agent.PromoteNode(ctx, &endpoints.PromoteNodeRequest{})
	if err != nil {
		return nil, err
	}

	return &endpoints.PromoteBackendResponse{
		Success: resp.Success,
		Message: resp.ErrorMessage,
	}, nil
}

func (m *Backend) DiscoverCluster(ctx context.Context, req *endpoints.DiscoverClusterRequest) (*endpoints.DiscoverClusterResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	// 1. Temporarily connect to primary to discover topology
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", req.PrimaryAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to primary for discovery: %w", err)
	}
	defer conn.Close()

	replicaAddrs, err := p.Handler.DiscoverTopology(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("topology discovery failed: %w", err)
	}

	// 2. Auto-add discovered replicas
	var added []string
	for _, addr := range replicaAddrs {
		// Check if already exists
		exists := false
		p.Mu.RLock()
		ps := p.Proxies[req.ProxyId]
		for _, b := range ps.Config.Backends {
			if b.Address == addr {
				exists = true
				break
			}
		}
		p.Mu.RUnlock()

		if !exists {
			addReq := &endpoints.AddBackendRequest{
				ProjectId: req.ProjectId,
				ProxyId:   req.ProxyId,
				Config: &domain.BackendConfig{
					Address: addr,
					Role:    string(pool2.RoleReplica),
				},
			}
			if _, err := m.AddBackend(ctx, addReq); err == nil {
				added = append(added, addr)
			}
		}
	}

	return &endpoints.DiscoverClusterResponse{
		DiscoveredNodes: replicaAddrs,
		AddedNodes:      added,
	}, nil
}

func (m *Backend) VacuumBackend(ctx context.Context, req *endpoints.VacuumBackendRequest, progress chan<- *endpoints.VacuumBackendProgress) error {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return err
	}

	p.Mu.RLock()
	ps, ok := p.Proxies[req.ProxyId]
	p.Mu.RUnlock()
	if !ok {
		return fmt.Errorf("proxy not found")
	}

	idx := slices.IndexFunc(ps.Backends, func(b pool2.Backend) bool {
		return b.Address() == req.Address
	})

	if idx == -1 {
		return fmt.Errorf("backend not found")
	}

	backend := ps.Backends[idx]
	agent := backend.AgentClient()
	if agent == nil {
		return fmt.Errorf("agent not connected for this backend")
	}

	stream, err := agent.VacuumDatabase(ctx, &endpoints.VacuumDatabaseRequest{
		Database: req.Database,
		Full:     req.Full,
		Analyze:  true,
	})
	if err != nil {
		return err
	}

	for {
		p, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		progress <- &endpoints.VacuumBackendProgress{
			Stage:      p.Stage,
			Percentage: p.Percentage,
			Message:    p.Message,
		}
	}

	return nil
}

func (m *Backend) GetAvailableVersions(ctx context.Context, req *endpoints.GetAvailableVersionsRequest) (*endpoints.GetAvailableVersionsResponse, error) {
	allVersions := make(map[string]struct{})

	// Collect versions from all active project states
	projects := m.registry.ListProjects()
	for _, pConfig := range projects {
		p, err := m.registry.GetProjectState(pConfig.Id)
		if err != nil {
			continue
		}

		p.Mu.RLock()
		for _, ps := range p.Proxies {
			for _, backend := range ps.Backends {
				if versions := backend.AvailableVersions(); len(versions) > 0 {
					for _, v := range versions {
						allVersions[v] = struct{}{}
					}
				}
			}
		}
		p.Mu.RUnlock()
	}

	// Fallback to hardcoded versions if none found from agents
	if len(allVersions) == 0 {
		return &endpoints.GetAvailableVersionsResponse{
			Versions: []string{"14", "15", "16", "17", "18"},
		}, nil
	}

	versions := slices.Collect(maps.Keys(allVersions))
	slices.Sort(versions)

	return &endpoints.GetAvailableVersionsResponse{
		Versions: versions,
	}, nil
}

func (m *Backend) GetAgentInfo(ctx context.Context, req *endpoints.GetAgentInfoRequest) (*endpoints.GetAgentInfoResponse, error) {
	client, err := orchestration.NewAgentClient(req.AgentAddress, req.AgentToken)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	info, err := client.GetSystemInfo(ctx)
	if err != nil {
		return nil, err
	}

	return &endpoints.GetAgentInfoResponse{
		Os:                 info.Os,
		Hostname:           info.Hostname,
		DetectedVersion:    info.DetectedVersion,
		AvailableVersions:  info.AvailableVersions,
		AgentVersion:       info.AgentVersion,
		RecommendedVersion: info.RecommendedVersion,
		PostgresRunning:    info.PostgresRunning,
		PostgresAddress:    info.PostgresAddress,
		PostgresDataDir:    info.PostgresDataDir,
		TuningSuggestions:  info.TuningSuggestions,
	}, nil
}

func (m *Backend) RestartBackendService(ctx context.Context, req *endpoints.RestartBackendServiceRequest) (*endpoints.RestartBackendServiceResponse, error) {
	backend, err := m.findBackend(req.ProjectId, req.ProxyId, req.Address)
	if err != nil {
		return nil, err
	}

	agent := backend.AgentClient()
	if agent == nil {
		return nil, fmt.Errorf("agent not connected")
	}

	resp, err := agent.RestartService(ctx, &endpoints.RestartServiceRequest{
		ServiceName: "postgresql",
	})
	if err != nil {
		return nil, err
	}

	return &endpoints.RestartBackendServiceResponse{
		Success:      resp.Success,
		ErrorMessage: resp.ErrorMessage,
	}, nil
}

func (m *Backend) ShutdownBackend(ctx context.Context, req *endpoints.ShutdownBackendRequest) (*endpoints.ShutdownBackendResponse, error) {
	backend, err := m.findBackend(req.ProjectId, req.ProxyId, req.Address)
	if err != nil {
		return nil, err
	}

	agent := backend.AgentClient()
	if agent == nil {
		return nil, fmt.Errorf("agent not connected")
	}

	resp, err := agent.ShutdownDatabase(ctx, &endpoints.ShutdownDatabaseRequest{})
	if err != nil {
		return nil, err
	}

	return &endpoints.ShutdownBackendResponse{
		Success:      resp.Success,
		ErrorMessage: resp.ErrorMessage,
	}, nil
}

func (m *Backend) findBackend(projectID, proxyID, address string) (pool2.Backend, error) {
	p, err := m.registry.GetProjectState(projectID)
	if err != nil {
		return nil, err
	}

	p.Mu.RLock()
	defer p.Mu.RUnlock()

	ps, ok := p.Proxies[proxyID]
	if !ok {
		return nil, fmt.Errorf("proxy not found")
	}

	for _, b := range ps.Backends {
		if b.Address() == address {
			return b, nil
		}
	}

	return nil, fmt.Errorf("backend not found")
}

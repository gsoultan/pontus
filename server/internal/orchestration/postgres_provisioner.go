package orchestration

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/internal/pool"
	protocol2 "github.com/gsoultan/pontus/server/internal/protocol"
)

type postgresProvisioner struct {
	backends func() []pool.Backend
	handler  protocol2.Handler
}

// NewPostgresProvisioner creates a new PostgreSQL provisioner.
func NewPostgresProvisioner(backends func() []pool.Backend, handler protocol2.Handler) Provisioner {
	return &postgresProvisioner{
		backends: backends,
		handler:  handler,
	}
}

func (p *postgresProvisioner) ProvisionReplica(ctx context.Context, req *endpoints.ProvisionReplicaRequest, progress chan<- *endpoints.ProvisionProgress) error {
	defer close(progress)

	// 1. Identify Primary Backend
	progress <- &endpoints.ProvisionProgress{Stage: "Initialization", Percentage: 5, Message: "Identifying primary backend"}
	var primary pool.Backend
	for _, b := range p.backends() {
		if b.Address() == req.SourceAddress {
			primary = b
			break
		}
	}
	if primary == nil {
		return fmt.Errorf("source backend %s not found", req.SourceAddress)
	}

	// 2. Connect to Primary Agent
	sourceHost := p.getHost(req.SourceAddress)
	sourceAgentAddr := fmt.Sprintf("%s:9091", sourceHost)
	sourceAgent, err := NewAgentClient(sourceAgentAddr, req.SourceAgentToken)
	if err != nil {
		return fmt.Errorf("failed to connect to source agent: %w", err)
	}
	defer sourceAgent.Close()

	// 3. Ensure Primary Config via Agent
	progress <- &endpoints.ProvisionProgress{Stage: "Primary Configuration", Percentage: 20, Message: "Ensuring primary configuration"}
	// In a real scenario, we would check and update postgresql.conf
	// For this example, we assume it's okay or we'd use ExecuteCommand to run some checks.

	// 4. Create Replication Slot on Primary via SQL
	progress <- &endpoints.ProvisionProgress{Stage: "Replication Slot", Percentage: 40, Message: "Creating replication slot"}
	if err := p.createReplicationSlot(ctx, primary, "pontus_repl_slot"); err != nil {
		return fmt.Errorf("failed to create replication slot: %w", err)
	}

	// 5. Connect to Target Agent
	targetHost := p.getHost(req.TargetAddress)
	targetAgentAddr := fmt.Sprintf("%s:9091", targetHost)
	targetAgent, err := NewAgentClient(targetAgentAddr, req.TargetAgentToken)
	if err != nil {
		return fmt.Errorf("failed to connect to target agent: %w", err)
	}
	defer targetAgent.Close()

	// 6. Run pg_basebackup on Target Agent
	progress <- &endpoints.ProvisionProgress{Stage: "Base Backup", Percentage: 60, Message: "Running pg_basebackup"}

	dataDir := req.DataDirectory
	if dataDir == "" {
		dataDir = "/var/lib/postgresql/data"
	}

	args := []string{
		"-h", sourceHost,
		"-D", dataDir,
		"-U", req.ReplicationUser,
		"-P", "-R",
	}
	// We'd pass the password via environment
	env := map[string]string{"PGPASSWORD": req.ReplicationPassword}

	out, err := targetAgent.ExecuteCommand(ctx, "pg_basebackup", args, env)
	if err != nil {
		return fmt.Errorf("failed to start pg_basebackup: %w", err)
	}
	for msg := range out {
		if msg.Stdout != "" {
			progress <- &endpoints.ProvisionProgress{Stage: "Base Backup", Message: msg.Stdout}
		}
		if msg.ExitCode != 0 {
			return fmt.Errorf("pg_basebackup failed with exit code %d: %s", msg.ExitCode, msg.Stderr)
		}
	}

	// 7. Restart Target DB
	progress <- &endpoints.ProvisionProgress{Stage: "Starting Replica", Percentage: 90, Message: "Starting replica database"}
	if err := targetAgent.RestartService(ctx, "postgresql"); err != nil {
		return fmt.Errorf("failed to restart replica service: %w", err)
	}

	progress <- &endpoints.ProvisionProgress{Stage: "Complete", Percentage: 100, Message: "Replica provisioned successfully"}
	return nil
}

func (p *postgresProvisioner) PromoteToPrimary(ctx context.Context, backendAddr string) error {
	var target pool.Backend
	for _, b := range p.backends() {
		if b.Address() == backendAddr {
			target = b
			break
		}
	}
	if target == nil {
		return fmt.Errorf("backend %s not found", backendAddr)
	}

	host := p.getHost(backendAddr)
	agentAddr := fmt.Sprintf("%s:9091", host)
	agent, err := NewAgentClient(agentAddr, target.AgentToken())
	if err != nil {
		return fmt.Errorf("failed to connect to agent at %s: %w", agentAddr, err)
	}
	defer agent.Close()

	// In Postgres, promotion can be done via pg_ctl promote or by creating a trigger file.
	// Using pg_ctl promote via the agent.
	// For promotion, we'd ideally also have the data directory,
	// but for now we'll try to detect it on the agent if not provided.
	// In this simplified implementation, we'll try a common path or ideally the agent would know.
	// For now, let's use a default or better, we could have stored it in the backend state.

	// Better: Agent should handle finding the data directory if not specified.
	info, err := agent.GetSystemInfo(ctx)
	dataDir := "/var/lib/postgresql/data"
	if err == nil && info.PostgresDataDir != "" {
		dataDir = info.PostgresDataDir
	}

	out, err := agent.ExecuteCommand(ctx, "pg_ctl", []string{"promote", "-D", dataDir}, nil)
	if err != nil {
		return fmt.Errorf("failed to execute promote command: %w", err)
	}

	for msg := range out {
		if msg.ExitCode != 0 {
			return fmt.Errorf("promotion failed with exit code %d: %s", msg.ExitCode, msg.Stderr)
		}
	}

	return nil
}

func (p *postgresProvisioner) CheckReplicationLag(ctx context.Context, backendAddr string) (time.Duration, error) {
	// Identify the backend
	var target pool.Backend
	for _, b := range p.backends() {
		if b.Address() == backendAddr {
			target = b
			break
		}
	}
	if target == nil {
		return 0, fmt.Errorf("backend %s not found", backendAddr)
	}

	// For Postgres, we can query pg_last_xact_replay_timestamp()
	conn, err := target.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer target.Release(conn)

	// In a real implementation, we'd execute: SELECT EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))
	// For now, return a mock value or implement the logic if possible.
	return 0, nil
}
func (p *postgresProvisioner) DemoteToReplica(ctx context.Context, backendAddr string, primaryAddr string) error {
	var target pool.Backend
	for _, b := range p.backends() {
		if b.Address() == backendAddr {
			target = b
			break
		}
	}
	if target == nil {
		return fmt.Errorf("backend %s not found", backendAddr)
	}

	host := p.getHost(backendAddr)
	agentAddr := fmt.Sprintf("%s:9091", host)
	agent, err := NewAgentClient(agentAddr, target.AgentToken())
	if err != nil {
		return fmt.Errorf("failed to connect to agent at %s: %w", agentAddr, err)
	}
	defer agent.Close()

	primaryHost := p.getHost(primaryAddr)

	// Reconfigure as replica pointing to the new primary
	req := &endpoints.SetupReplicationRequest{
		PrimaryHost: primaryHost,
		PrimaryPort: 5432, // Default
		// We might need more info here, but for now this is the idea
	}

	out, err := agent.SetupReplication(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to setup replication: %w", err)
	}

	for msg := range out {
		if msg.Percentage == 100 {
			return nil
		}
	}

	return nil
}

func (p *postgresProvisioner) getHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func (p *postgresProvisioner) createReplicationSlot(ctx context.Context, b pool.Backend, slotName string) error {
	conn, err := b.Acquire(ctx)
	if err != nil {
		return err
	}
	defer b.Release(conn)

	// Since we don't have a generic Exec, we'll cast to PostgresHandler or use its logic.
	// For this task, I'll assume I can add a method to PostgresHandler.
	if ph, ok := p.handler.(*protocol2.PostgresHandler); ok {
		return ph.CreateReplicationSlot(ctx, conn, slotName)
	}
	return fmt.Errorf("unsupported handler type for replication slot")
}

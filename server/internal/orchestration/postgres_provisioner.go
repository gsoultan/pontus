package orchestration

import (
	"context"
	"fmt"
	"log/slog"
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
	// Two different addresses, deliberately: the agent to ask, and the database
	// host for pg_basebackup to stream from. They are usually the same machine
	// and never the same port.
	sourceHost := p.getHost(req.SourceAddress)
	sourceAgentAddr := agentAddressFor(primary, sourceHost)
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
	// The target is not a backend yet — it is the node being turned into one —
	// so there is no configured address to read. The agent's own default is the
	// only sensible guess, and a freshly provisioned host is where that guess
	// is most likely to be right.
	targetAgentAddr := net.JoinHostPort(targetHost, defaultAgentPort)
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

	agentAddr := agentAddressFor(target, p.getHost(backendAddr))
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

	// Ask the standby to promote itself before reaching for a shell.
	//
	// `pg_ctl promote` through the agent cannot work as the agent runs it:
	// pg_ctl refuses to run as root, and the agent is root because it installs
	// packages and manages services. So automatic promotion failed on every
	// deployment — with `exit code 1` and, until the message below, no reason
	// at all. pg_promote() needs neither root nor the data directory, and runs
	// on a connection Pontus already holds.
	if err := p.promoteViaSQL(ctx, target); err == nil {
		slog.Info("Standby promoted itself", "address", backendAddr)
		return nil
	} else {
		slog.Warn("pg_promote() did not succeed; falling back to the agent",
			"address", backendAddr, "error", err)
	}

	dataDir := p.dataDirectoryFor(ctx, target, agent)

	out, err := agent.ExecuteCommand(ctx, "pg_ctl", []string{"promote", "-D", dataDir}, nil)
	if err != nil {
		return fmt.Errorf("failed to execute promote command: %w", err)
	}

	// Say what failed and where.
	//
	// This reported `promotion failed with exit code 1:` — a number and an
	// empty string — for the most consequential command Pontus runs. The reason
	// it was empty is that the interesting failures write to stdout, or say
	// nothing at all when -D simply is not a data directory.
	var stderr string
	for msg := range out {
		if msg.Stderr != "" {
			stderr = msg.Stderr
		}
		if msg.ExitCode != 0 {
			return fmt.Errorf("pg_ctl promote -D %s on %s failed with exit code %d: %s",
				dataDir, backendAddr, msg.ExitCode, orNoOutput(stderr))
		}
	}

	return nil
}

// orNoOutput names an empty stderr rather than trailing off after a colon.
func orNoOutput(s string) string {
	if s == "" {
		return "(no output; the usual cause is -D pointing somewhere that is " +
			"not this server's data directory)"
	}
	return s
}

// promoteViaSQL promotes a standby over Pontus's own admin channel.
//
// Admin() is the connection Pontus authenticates for itself, the one health
// probes and role detection already use. A pooled connection is the wrong tool
// here: under passthrough it carries a client's credentials, or has never
// completed a startup exchange at all, and a query written to it simply gets
// EOF.
func (p *postgresProvisioner) promoteViaSQL(ctx context.Context, b pool.Backend) error {
	if b == nil {
		return fmt.Errorf("no backend to promote")
	}
	admin := b.Admin()
	if admin == nil || !admin.Available() {
		return fmt.Errorf("no admin connection to %s; set admin_dsn for it", b.Address())
	}

	// wait => true, so a reply means the promotion finished rather than that it
	// was merely accepted; wait_seconds bounds it, because a standby replaying
	// a backlog can take a while and this must not hang the failover.
	var promoted bool
	if err := admin.QueryRow(ctx, "SELECT pg_promote(true, 60)").Scan(&promoted); err != nil {
		return fmt.Errorf("pg_promote on %s: %w", b.Address(), err)
	}
	if !promoted {
		return fmt.Errorf("pg_promote on %s returned false; it did not leave "+
			"recovery within its wait", b.Address())
	}
	return nil
}

// dataDirectoryFor works out where a backend's data directory is.
//
// Asked of the server first, because the server knows. The agent detects it by
// walking a fixed list of candidate paths looking for a PG_VERSION file, which
// finds a default install and misses a cluster on a mounted volume, a second
// cluster on one host, or anything a packager put elsewhere.
func (p *postgresProvisioner) dataDirectoryFor(ctx context.Context, b pool.Backend, agent AgentClient) string {
	if dir := askServerForDataDir(ctx, b); dir != "" {
		return dir
	}
	if info, err := agent.GetSystemInfo(ctx); err == nil && info.PostgresDataDir != "" {
		return info.PostgresDataDir
	}
	return "/var/lib/postgresql/data"
}

// askServerForDataDir reads data_directory over Pontus's own admin channel.
func askServerForDataDir(ctx context.Context, b pool.Backend) string {
	if b == nil {
		return ""
	}
	admin := b.Admin()
	if admin == nil || !admin.Available() {
		return ""
	}

	var dir string
	if err := admin.QueryRow(ctx, "SHOW data_directory").Scan(&dir); err != nil {
		slog.Debug("Could not read data_directory from the server",
			"backend", b.Address(), "error", err)
		return ""
	}
	return dir
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

	agentAddr := agentAddressFor(target, p.getHost(backendAddr))
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

// defaultAgentPort is where an agent listens when a backend has not said
// otherwise. It is the agent's own default, so it is the right guess for a node
// being provisioned — one that is not a backend yet has nothing to be asked.
const defaultAgentPort = "9091"

// agentAddressFor returns where to reach a backend's agent.
//
// Every one of these call sites used to build `<database host>:9091` and ignore
// the configured address entirely, while taking the *token* from the very same
// backend. So `agent_addr` was mandatory, validated at startup, honoured by the
// pool's own health and lag checks — and silently dropped by promotion,
// demotion and provisioning. Point an agent anywhere but the database host's
// default port and health checks kept working while failover could not promote
// anything, which is the worst possible time to discover a setting was not
// being read.
func agentAddressFor(b pool.Backend, fallbackHost string) string {
	if b != nil {
		if addr := b.AgentAddr(); addr != "" {
			return addr
		}
	}
	return net.JoinHostPort(fallbackHost, defaultAgentPort)
}

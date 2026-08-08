package infrastructure

import (
	"context"
	"log/slog"
	"os"

	"github.com/gsoultan/pontus/agent/infrastructure/validator"
	"github.com/gsoultan/pontus/agent/services"
	"github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/pkg/system"
)

// agentService implements the services.Service interface by composing monitor and management.
type agentService struct {
	services.Monitor
	services.Management
}

func (s *agentService) Start(ctx context.Context) error {
	// For now only monitor needs to start something
	if m, ok := s.Monitor.(interface{ Start(context.Context) error }); ok {
		return m.Start(ctx)
	}
	return nil
}

// DefaultLocalDSN is the connection string used when none is configured.
//
// The agent runs on the database host and reads pg_stat_* for host metrics, so
// a local socket with no password is the normal case. It carries no
// credentials: the previous default embedded postgres:postgres, which is a
// working credential on any cluster that kept the default superuser password.
const DefaultLocalDSN = "postgres:///postgres?host=/var/run/postgresql&sslmode=disable"

// NewService creates a new instance of the agent service.
//
// The metrics DSN comes from PONTUS_AGENT_DSN when set. Metrics collection is
// optional: if the collector cannot be built the agent still serves every
// other RPC rather than refusing to start, because provisioning and service
// control matter more than pg_stat_* sampling.
func NewService() services.Service {
	dsn := os.Getenv("PONTUS_AGENT_DSN")
	if dsn == "" {
		dsn = DefaultLocalDSN
	}

	collector, err := observability.NewPostgresCollector(dsn)
	if err != nil {
		slog.Warn("Agent metrics collector unavailable; host metrics will be empty",
			"error", err)
	}

	repoManager := NewAptManager()
	monitorImpl := NewMonitor(collector, repoManager)
	managementImpl := NewManagement(
		system.GetPostgresDataDirs(),
		map[string]services.Validator{
			"pg_hba.conf": &validator.Postgres{},
		},
		repoManager,
	)

	return &agentService{
		Monitor:    monitorImpl,
		Management: managementImpl,
	}
}

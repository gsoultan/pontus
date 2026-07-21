package infrastructure

import (
	"context"

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

// NewService creates a new instance of the agent service.
func NewService() services.Service {
	// In a real app, the DSN would come from config
	collector, _ := observability.NewPostgresCollector("postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")

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

package services

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Monitor defines the interface for host monitoring operations.
type Monitor interface {
	// GetSystemInfo retrieves host system information.
	GetSystemInfo(ctx context.Context) (*endpoints.GetSystemInfoResponse, error)

	// StreamLogs streams the logs of a database instance.
	StreamLogs(ctx context.Context, req *endpoints.LogStreamRequest) (<-chan *endpoints.LogStreamResponse, error)

	// GetPostgresInsights retrieves database-specific insights for Postgres.
	GetPostgresInsights(ctx context.Context, req *endpoints.GetPostgresInsightsRequest) (*endpoints.GetPostgresInsightsResponse, error)
}

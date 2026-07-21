package services

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Postgres defines the interface for database-specific insights.
type Postgres interface {
	GetPostgresInsights(ctx context.Context, req *endpoints.GetPostgresInsightsRequest) (*endpoints.GetPostgresInsightsResponse, error)
}

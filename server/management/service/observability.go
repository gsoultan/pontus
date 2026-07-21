package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Observability defines the interface for system observability.
type Observability interface {
	GetStatus(ctx context.Context, req *endpoints.GetStatusRequest) (*endpoints.GetStatusResponse, error)
	StreamLogs(ctx context.Context, req *endpoints.StreamLogsRequest, logs chan<- *domain.LogEntry) error
	Explain(ctx context.Context, projectID string, query string) (string, error)
	ExplainQuery(ctx context.Context, req *endpoints.ExplainQueryRequest) (*endpoints.ExplainQueryResponse, error)
	GetLogs(ctx context.Context, req *endpoints.GetLogsRequest) (*endpoints.GetLogsResponse, error)
	GetMetricsHistory(ctx context.Context, req *endpoints.GetMetricsHistoryRequest) (*endpoints.GetMetricsHistoryResponse, error)
	GetTopQueriesHistory(ctx context.Context, req *endpoints.GetTopQueriesHistoryRequest) (*endpoints.GetTopQueriesHistoryResponse, error)
	TuneDatabase(ctx context.Context, req *endpoints.TuneDatabaseRequest) (*endpoints.TuneDatabaseResponse, error)
	ApplyTuning(ctx context.Context, req *endpoints.ApplyTuningRequest) (*endpoints.ApplyTuningResponse, error)
	GetPostgresInsights(ctx context.Context, req *endpoints.GetBackendPostgresInsightsRequest) (*endpoints.GetBackendPostgresInsightsResponse, error)
}

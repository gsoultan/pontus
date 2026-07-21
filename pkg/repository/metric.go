package repository

import (
	"context"
	"time"

	"github.com/gsoultan/pontus/pkg/observability"
)

// Metric defines the persistence operations for metric snapshots.
type Metric interface {
	SaveSnapshot(ctx context.Context, projectID string, s observability.MetricSnapshot) error
	GetHistory(ctx context.Context, projectID string, start, end time.Time) ([]observability.MetricSnapshot, error)
	Prune(ctx context.Context, before time.Time) error
}

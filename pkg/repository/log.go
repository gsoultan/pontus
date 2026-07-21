package repository

import (
	"context"
	"iter"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
)

// Log defines the persistence operations for system and query logs.
type Log interface {
	Append(ctx context.Context, projectID string, entry *domain.LogEntry) error
	Stream(ctx context.Context, projectID string) iter.Seq[*domain.LogEntry]
	Prune(ctx context.Context, before time.Time) error
}

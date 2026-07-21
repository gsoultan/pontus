package insights

import (
	"context"
	"net"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// TuningResult represents the output of a tuning operation.
type TuningResult struct {
	Suggestions  []*domain.TuningSuggestion
	SystemChecks []string
}

// Tuner provides database-specific tuning recommendations.
type Tuner interface {
	Tune(ctx context.Context, metrics *domain.SystemMetrics) TuningResult
	Apply(ctx context.Context, handler protocol.Handler, conn net.Conn, suggestion *domain.TuningSuggestion) error
}

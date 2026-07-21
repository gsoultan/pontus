package protocol

import (
	"context"
	"net"

	"github.com/gsoultan/pontus/api/proto/domain"
)

// MetricsCollector defines protocol-specific operations for collecting database metrics.
type MetricsCollector interface {
	// CollectMetrics gathers detailed metrics from the database.
	CollectMetrics(ctx context.Context, conn net.Conn) (*domain.DatabaseMetrics, error)
}

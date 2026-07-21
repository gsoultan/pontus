package insights

import (
	"context"
	"fmt"
	"net"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// Postgres implements the Tuner interface for PostgreSQL databases.
type Postgres struct {
}

// NewPostgres creates a new Postgres tuner.
func NewPostgres() *Postgres {
	return &Postgres{}
}

// Tune analyzes system metrics and provides PostgreSQL-specific tuning suggestions.
func (p *Postgres) Tune(_ context.Context, metrics *domain.SystemMetrics) TuningResult {
	if metrics == nil || metrics.MemoryTotalBytes == 0 {
		return TuningResult{}
	}

	res := TuningResult{}
	ram := metrics.MemoryTotalBytes
	cores := uint64(metrics.CpuCores)
	if cores == 0 {
		cores = 1
	}

	// 1. shared_buffers: Generally 25% of total RAM
	sharedBuffers := ram / 4
	res.Suggestions = append(res.Suggestions, &domain.TuningSuggestion{
		Parameter:      "shared_buffers",
		SuggestedValue: p.formatBytes(sharedBuffers),
		Reason:         "Should be around 25% of total system memory for most workloads.",
	})

	// 2. effective_cache_size: Generally 75% of total RAM
	effectiveCache := (ram * 3) / 4
	res.Suggestions = append(res.Suggestions, &domain.TuningSuggestion{
		Parameter:      "effective_cache_size",
		SuggestedValue: p.formatBytes(effectiveCache),
		Reason:         "Helps the planner estimate how much memory is available for caching data.",
	})

	// 3. work_mem: RAM / (max_connections * 2) or similar
	// Assume default max_connections = 100 for calculation if not known
	maxConns := uint64(100)
	workMem := (ram - sharedBuffers) / (maxConns * 2)
	if workMem > 64*1024*1024 { // Cap at 64MB per connection by default to be safe
		workMem = 64 * 1024 * 1024
	}
	res.Suggestions = append(res.Suggestions, &domain.TuningSuggestion{
		Parameter:      "work_mem",
		SuggestedValue: p.formatBytes(workMem),
		Reason:         "Allocated per-sort operation. Calculated based on available RAM and expected connections.",
	})

	// 4. maintenance_work_mem: 1/16 of RAM
	maintWorkMem := ram / 16
	if maintWorkMem > 2*1024*1024*1024 { // Cap at 2GB
		maintWorkMem = 2 * 1024 * 1024 * 1024
	}
	res.Suggestions = append(res.Suggestions, &domain.TuningSuggestion{
		Parameter:      "maintenance_work_mem",
		SuggestedValue: p.formatBytes(maintWorkMem),
		Reason:         "Used for maintenance tasks like VACUUM and CREATE INDEX.",
	})

	// 5. max_parallel_workers: equal to cores
	res.Suggestions = append(res.Suggestions, &domain.TuningSuggestion{
		Parameter:      "max_parallel_workers",
		SuggestedValue: fmt.Sprintf("%d", cores),
		Reason:         "Should match the number of available CPU cores.",
	})

	// System Checks
	if metrics.OpenFilesLimit > 0 && metrics.OpenFilesLimit < 65536 {
		res.SystemChecks = append(res.SystemChecks, fmt.Sprintf("ulimit -n is currently %d; recommended at least 65536 for high-concurrency database workloads.", metrics.OpenFilesLimit))
	}

	if metrics.MaxProcessesLimit > 0 && metrics.MaxProcessesLimit < 32768 {
		res.SystemChecks = append(res.SystemChecks, fmt.Sprintf("ulimit -u is currently %d; recommended at least 32768 to avoid process exhaustion.", metrics.MaxProcessesLimit))
	}

	return res
}

// Apply applies a tuning suggestion to the PostgreSQL database.
func (p *Postgres) Apply(ctx context.Context, handler protocol.Handler, conn net.Conn, suggestion *domain.TuningSuggestion) error {
	// 1. Apply the setting using ALTER SYSTEM
	query := fmt.Sprintf("ALTER SYSTEM SET %s = '%s'", suggestion.Parameter, suggestion.SuggestedValue)
	if err := handler.Execute(ctx, conn, query); err != nil {
		return fmt.Errorf("failed to apply setting: %w", err)
	}

	// 2. Try to reload configuration
	// Note: Some settings (like shared_buffers) require a restart.
	// pg_reload_conf() returns true even if some settings couldn't be applied until restart.
	if err := handler.Execute(ctx, conn, "SELECT pg_reload_conf()"); err != nil {
		return fmt.Errorf("failed to reload configuration: %w", err)
	}

	return nil
}

func (p *Postgres) formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	val := float64(b) / float64(div)
	unitChar := "KMGTPE"[exp]

	// PostgreSQL accepts 'GB', 'MB', etc.
	return fmt.Sprintf("%.0f%cB", val, unitChar)
}

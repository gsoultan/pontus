package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	PoolSaturation = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_pool_saturation",
		Help: "Percentage of MaxConns used per backend",
	}, []string{"address"})

	BackendLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "pontus_backend_latency_seconds",
		Help:    "Latency of queries per backend",
		Buckets: prometheus.DefBuckets,
	}, []string{"address"})

	QueriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pontus_queries_total",
		Help: "Total number of queries processed",
	}, []string{"address", "type", "result"})

	CPUUsage = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pontus_system_cpu_usage_percent",
		Help: "Current system CPU usage in percent",
	})

	MemoryUsage = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pontus_system_memory_usage_percent",
		Help: "Current system memory usage in percent",
	})

	StorageUsage = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pontus_system_storage_usage_percent",
		Help: "Current system storage usage in percent",
	})

	Load1 = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pontus_system_load1",
		Help: "System load average over the last 1 minute",
	})

	// Database node metrics
	ActiveBackends = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_active_backends",
		Help: "Number of active backends on the database node",
	}, []string{"address"})

	MaxBackends = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_max_backends",
		Help: "Maximum number of backends allowed on the database node",
	}, []string{"address"})

	TransactionsCommitted = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_transactions_committed_total",
		Help: "Total number of transactions committed on the database node",
	}, []string{"address"})

	TransactionsRolledBack = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_transactions_rolled_back_total",
		Help: "Total number of transactions rolled back on the database node",
	}, []string{"address"})

	BlocksRead = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_blocks_read_total",
		Help: "Total number of disk blocks read on the database node",
	}, []string{"address"})

	BlocksHit = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_blocks_hit_total",
		Help: "Total number of disk blocks found in buffer cache on the database node",
	}, []string{"address"})

	CacheHitRatio = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_cache_hit_ratio",
		Help: "Buffer cache hit ratio on the database node",
	}, []string{"address"})

	Conflicts = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_conflicts_total",
		Help: "Total number of queries cancelled due to conflicts with recovery on the database node",
	}, []string{"address"})

	Deadlocks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_deadlocks_total",
		Help: "Total number of deadlocks detected on the database node",
	}, []string{"address"})

	ReplicationLagBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_db_replication_lag_bytes",
		Help: "Replication lag in bytes on the database node",
	}, []string{"address"})
)

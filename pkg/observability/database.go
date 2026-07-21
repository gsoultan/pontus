package observability

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	_ "github.com/lib/pq"
)

// DatabaseMetricsData holds metrics collected from the database.
type DatabaseMetricsData struct {
	ActiveBackends         int64
	MaxBackends            int64
	TransactionsCommitted  int64
	TransactionsRolledBack int64
	BlocksRead             int64
	BlocksHit              int64
	CacheHitRatio          float64
	Conflicts              int64
	Deadlocks              int64
	ReplicationLagBytes    int64
	IsRecovery             bool
}

// DatabaseCollector defines the interface for collecting database metrics.
type DatabaseCollector interface {
	Collect(ctx context.Context) (DatabaseMetricsData, error)
	GetInsights(ctx context.Context) ([]*domain.QueryInsight, []*domain.LockInsight, []*domain.ReplicationInsight, error)
	Close() error
}

type postgresCollector struct {
	db *sql.DB

	mu          sync.Mutex
	cachedData  DatabaseMetricsData
	cachedTime  time.Time
	cacheMaxAge time.Duration
}

// NewPostgresCollector creates a new collector for PostgreSQL.
func NewPostgresCollector(dsn string) (DatabaseCollector, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(time.Minute)

	return &postgresCollector{
		db:          db,
		cacheMaxAge: time.Second,
	}, nil
}

func (c *postgresCollector) Collect(ctx context.Context) (DatabaseMetricsData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.cachedTime) < c.cacheMaxAge {
		return c.cachedData, nil
	}

	data := DatabaseMetricsData{}

	// Basic stats from pg_stat_database
	query := `
		SELECT 
			sum(numbackends), 
			sum(xact_commit), 
			sum(xact_rollback), 
			sum(blks_read), 
			sum(blks_hit),
			sum(conflicts),
			sum(deadlocks)
		FROM pg_stat_database`

	err := c.db.QueryRowContext(ctx, query).Scan(
		&data.ActiveBackends,
		&data.TransactionsCommitted,
		&data.TransactionsRolledBack,
		&data.BlocksRead,
		&data.BlocksHit,
		&data.Conflicts,
		&data.Deadlocks,
	)
	if err != nil {
		return data, fmt.Errorf("failed to query pg_stat_database: %w", err)
	}

	if data.BlocksRead+data.BlocksHit > 0 {
		data.CacheHitRatio = float64(data.BlocksHit) / float64(data.BlocksRead+data.BlocksHit)
	}

	// Max backends
	c.db.QueryRowContext(ctx, "SHOW max_connections").Scan(&data.MaxBackends)

	// Recovery status
	c.db.QueryRowContext(ctx, "SELECT pg_is_in_recovery()").Scan(&data.IsRecovery)

	// Replication lag
	if data.IsRecovery {
		c.db.QueryRowContext(ctx, "SELECT COALESCE(pg_wal_lsn_diff(pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn()), 0)").Scan(&data.ReplicationLagBytes)
	}

	c.cachedData = data
	c.cachedTime = time.Now()
	return data, nil
}

func (c *postgresCollector) GetInsights(ctx context.Context) ([]*domain.QueryInsight, []*domain.LockInsight, []*domain.ReplicationInsight, error) {
	queries := []*domain.QueryInsight{}
	locks := []*domain.LockInsight{}
	replication := []*domain.ReplicationInsight{}

	// Top queries from pg_stat_statements
	qRows, err := c.db.QueryContext(ctx, `
		SELECT query, calls, total_exec_time, min_exec_time, max_exec_time, mean_exec_time, rows 
		FROM pg_stat_statements 
		ORDER BY total_exec_time DESC 
		LIMIT 10`)
	if err == nil {
		defer qRows.Close()
		for qRows.Next() {
			qi := &domain.QueryInsight{}
			if err := qRows.Scan(&qi.Query, &qi.Calls, &qi.TotalTime, &qi.MinTime, &qi.MaxTime, &qi.MeanTime, &qi.Rows); err == nil {
				queries = append(queries, qi)
			}
		}
	}

	// Active locks
	lRows, err := c.db.QueryContext(ctx, `
		SELECT pid, locktype, mode, granted, COALESCE(query, '')
		FROM pg_locks l
		LEFT JOIN pg_stat_activity a ON l.pid = a.pid
		WHERE NOT granted
		LIMIT 20`)
	if err == nil {
		defer lRows.Close()
		for lRows.Next() {
			li := &domain.LockInsight{}
			if err := lRows.Scan(&li.Pid, &li.Locktype, &li.Mode, &li.Granted, &li.Query); err == nil {
				locks = append(locks, li)
			}
		}
	}

	// Replication status
	rRows, err := c.db.QueryContext(ctx, `
		SELECT pid, state, 
			pg_wal_lsn_diff(pg_current_wal_lsn(), sent_lsn)::text,
			pg_wal_lsn_diff(pg_current_wal_lsn(), write_lsn)::text,
			pg_wal_lsn_diff(pg_current_wal_lsn(), flush_lsn)::text,
			pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn)::text
		FROM pg_stat_replication`)
	if err == nil {
		defer rRows.Close()
		for rRows.Next() {
			ri := &domain.ReplicationInsight{}
			if err := rRows.Scan(&ri.Pid, &ri.State, &ri.SentLsn, &ri.WriteLsn, &ri.FlushLsn, &ri.ReplayLsn); err == nil {
				replication = append(replication, ri)
			}
		}
	}

	return queries, locks, replication, nil
}

func (c *postgresCollector) Close() error {
	return c.db.Close()
}

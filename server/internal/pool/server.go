package pool

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/gpool/pkg/pooling"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service"
	"github.com/gsoultan/pontus/internal/system"
	"github.com/gsoultan/pontus/pkg/observability"
	health2 "github.com/gsoultan/pontus/server/internal/health"
	"github.com/gsoultan/pontus/server/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Server manages a pool of connections to a single backend server.
type Server struct {
	address string
	zone    string
	roleMu  sync.RWMutex
	role    Role
	core    *pooling.Core[*Conn]

	// admin is Pontus's own authenticated channel, used for the questions the
	// control plane asks a database. Nil when no admin_dsn is configured.
	admin *AdminSession
	// checkedOut counts connections currently held by a caller. The engine's
	// Stat samples its total and its shard counters independently, so an active
	// count derived from them can read high while the background warm-up is in
	// flight — and the balancer's cost function multiplies by this number.
	checkedOut     atomic.Int64
	maxConns       int32
	minConns       int32
	healthy        atomic.Bool
	dialTimeout    time.Duration
	maxIdleTime    time.Duration
	maxLifetime    time.Duration
	minIdle        int32
	handler        protocol.Handler
	tlsConfig      *tls.Config
	latency        atomic.Int64 // nanoseconds
	rtt            atomic.Int64 // nanoseconds
	lastHealthy    atomic.Value // time.Time
	breaker        health2.Breaker
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.RWMutex
	weight         int
	waitTimeout    time.Duration
	replicationLag atomic.Int64 // nanoseconds
	draining       atomic.Bool
	roleCheckChan  chan struct{}

	// replicating tracks whether a replica has a WAL receiver attached, and
	// replicatingSince when that run of streaming began — see reattach.go.
	replicating        atomic.Bool
	replicatingSince   atomic.Int64
	totalRequests      atomic.Int64
	totalErrors        atomic.Int64
	errorRate          atomic.Uint64 // bits of float64
	agentAddr          string
	agentToken         string
	agentClient        service.AgentServiceClient
	agentConn          *grpc.ClientConn
	monitor            *system.Monitor
	controller         *AdaptivePoolController
	dbMetrics          atomic.Pointer[domain.DatabaseMetrics]
	installedVersion   atomic.Value // string
	recommendedVersion atomic.Value // string
	availableVersions  atomic.Value // []string
}

var (
	ErrPoolExhausted = errors.New("connection pool exhausted")
	ErrPoolClosed    = errors.New("connection pool closed")
	ErrDraining      = errors.New("backend is draining")
)

// NewServer creates a new Server for the given address and role.
func NewServer(address string, zone string, agentAddr string, agentToken string, role Role, weight int, maxConns int32, minIdle int32, dialTimeout time.Duration, handler protocol.Handler, tlsConfig *tls.Config, monitor *system.Monitor, adminDSN string) (*Server, error) {
	if agentAddr == "" {
		return nil, fmt.Errorf("agent address is mandatory for backend %s", address)
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Server{
		address:     address,
		zone:        zone,
		agentAddr:   agentAddr,
		agentToken:  agentToken,
		role:        role,
		weight:      weight,
		maxConns:    maxConns,
		minConns:    minIdle,
		dialTimeout: dialTimeout,
		maxIdleTime: 5 * time.Minute,
		maxLifetime: 1 * time.Hour,
		minIdle:     minIdle,
		handler:     handler,
		tlsConfig:   tlsConfig,
		breaker:     health2.NewCircuitBreaker(5, 30*time.Second),
		ctx:         ctx,
		cancel:      cancel,
		waitTimeout: 5 * time.Second,
		monitor:     monitor,
	}

	// Pontus's own channel to this backend. Optional: without it the health
	// probe and role detection fall back to running on a pooled connection,
	// which only works when a client has already authenticated one.
	admin, err := NewAdminSession(adminDSN, dialTimeout)
	if err != nil {
		slog.Warn("Admin session unavailable; health checks and role detection "+
			"will fall back to client connections",
			"address", address, "error", err)
	}
	p.admin = admin

	p.controller = NewAdaptivePoolController(p, monitor)

	// The engine owns capacity, idle buckets, the reaper and statistics. MaxConns
	// is a structural ceiling here — a connection is only created while its
	// acquirer holds a permit — rather than the check-then-act the previous
	// implementation used, which two concurrent acquirers could both pass.
	core, err := pooling.New[*Conn](&connDriver{
		address:     address,
		dialTimeout: dialTimeout,
		tlsConfig:   tlsConfig,
		handler:     handler,
	}, pooling.Config{
		MaxConns: maxConns,
		// The adaptive controller may lower capacity under pressure and raise it
		// again, but never above the configured max_conns.
		MaxConnsLimit:     maxConns,
		MinConns:          minIdle,
		MaxConnIdleTime:   p.maxIdleTime,
		MaxConnLifetime:   p.maxLifetime,
		HealthCheckPeriod: time.Minute,
		ConnectTimeout:    dialTimeout,
	}.WithDefaults())
	if err != nil {
		cancel()
		return nil, fmt.Errorf("pool for backend %s: %w", address, err)
	}
	p.core = core

	p.roleCheckChan = make(chan struct{}, 1)
	p.healthy.Store(true)

	var opts []grpc.DialOption
	if tlsConfig != nil {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if agentToken != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", agentToken)
			return invoker(ctx, method, req, reply, cc, opts...)
		}), grpc.WithStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", agentToken)
			return streamer(ctx, desc, cc, method, opts...)
		}))
	}

	conn, err := grpc.NewClient(agentAddr, opts...)
	if err == nil {
		p.agentConn = conn
		p.agentClient = service.NewAgentServiceClient(conn)
	} else {
		slog.Error("Failed to connect to agent", "address", agentAddr, "error", err)
	}

	return p, nil
}

// Acquire gets a connection from the pool, creating one if capacity allows.
//
// The wait, the capacity ceiling and the idle-vs-fresh decision all belong to
// the engine now; what stays here is Pontus-specific: refusing a draining
// backend and keeping dial failures behind the circuit breaker.
func (p *Server) Acquire(ctx context.Context) (net.Conn, error) {
	if p.IsDraining() {
		return nil, ErrDraining
	}

	waitCtx, cancel := context.WithTimeoutCause(ctx, p.waitTimeout, ErrPoolExhausted)
	defer cancel()

	var handle pooling.Handle[*Conn]
	err := p.breaker.Call(waitCtx, func() error {
		var aerr error
		handle, aerr = p.core.Acquire(waitCtx)
		return aerr
	})
	if err != nil {
		if cause := context.Cause(waitCtx); cause != nil && errors.Is(cause, ErrPoolExhausted) {
			return nil, cause
		}
		return nil, err
	}

	conn := handle.Conn()
	// Store the one copy of the handle; Release goes back through it.
	conn.handle = handle
	conn.IncUseCount()
	p.checkedOut.Add(1)
	return conn, nil
}

// ReportResult records the outcome of a request.
//
// This used to halve pool capacity on every error (AIMD). That made capacity
// remotely controllable: proxyResponse reports a *client* write failure as an
// error, so a client that connected, sent a query and disconnected in a loop
// drove every other tenant's ceiling down to minConns. Capacity is now a fixed
// structural bound owned by the engine, and this only feeds the error rate.
func (p *Server) ReportResult(err error) {
	if err != nil {
		p.IncErrors()
	}
}

func (p *Server) AgentClient() service.AgentServiceClient {
	return p.agentClient
}

func (p *Server) AgentToken() string {
	return p.agentToken
}

// Release returns a connection to the pool.
//
// A connection that is not ours never came from the engine, so closing it is the
// only correct disposal. Everything else — the recycle gate, idle bookkeeping,
// permit return — happens inside Handle.Release, which is idempotent.
func (p *Server) Release(conn net.Conn) error {
	c, ok := conn.(*Conn)
	if !ok {
		return conn.Close()
	}
	p.checkedOut.Add(-1)

	if p.IsDraining() {
		// Do not put it back; a draining backend should shed connections.
		c.MarkBroken()
	}

	c.handle.Release()

	if p.IsDraining() && p.ActiveConns() == 0 {
		p.SetDraining(false) // Finished draining
	}
	return nil
}

// IsHealthy returns the current health status.
func (p *Server) IsHealthy() bool {
	return p.healthy.Load()
}

// Address returns the server address.
func (p *Server) Address() string {
	return p.address
}

// Zone returns the server zone.
func (p *Server) Zone() string {
	return p.zone
}

func (p *Server) Weight() int {
	return p.weight
}

func (p *Server) SetWeight(weight int) {
	p.mu.Lock()
	p.weight = weight
	p.mu.Unlock()
}

// SetMaxConns adjusts the pool ceiling within the configured max_conns. It does
// not block, and a value outside [min_idle, max_conns] is refused rather than
// clamped, so a miscalculated target is visible instead of silently ignored.
func (p *Server) SetMaxConns(n int32) error {
	return p.core.SetMaxConns(n)
}

// ActiveConns returns the number of connections currently checked out.
func (p *Server) ActiveConns() int64 {
	return p.checkedOut.Load()
}

// Role returns the backend role.
func (p *Server) Role() Role {
	p.roleMu.RLock()
	defer p.roleMu.RUnlock()
	return p.role
}

func (p *Server) setRole(role Role) {
	p.roleMu.Lock()
	old := p.role
	p.role = role
	p.roleMu.Unlock()

	if old == RolePrimary && role == RoleReplica {
		p.SetDraining(true)
		// Clear idle connections because they might have been used for writes
		// and we want fresh connections for the new role.
		p.clearIdleConns()
	}
}

// clearIdleConns discards every pooled connection, so a backend that has just
// changed role does not hand a connection opened against the old role to the
// next caller. Connections still checked out are judged when they are released.
func (p *Server) clearIdleConns() {
	if n := p.core.EvictIdle(); n > 0 {
		slog.Info("Discarded pooled connections after role change", "address", p.address, "count", n)
	}
}

// Latency returns the current estimated latency of the backend.
func (p *Server) Latency() time.Duration {
	return time.Duration(p.latency.Load())
}

func (p *Server) RTT() time.Duration {
	return time.Duration(p.rtt.Load())
}

// ErrorRate returns the current estimated error rate.
func (p *Server) ErrorRate() float64 {
	return math.Float64frombits(p.errorRate.Load())
}

// ReplicationLag returns the current estimated replication lag.
func (p *Server) ReplicationLag() time.Duration {
	return time.Duration(p.replicationLag.Load())
}

// ReportLatency updates the latency estimate using EWMA.
func (p *Server) ReportLatency(d time.Duration) {
	// alpha = 0.1 for smoothing
	const alpha = 0.1
	old := p.latency.Load()
	if old == 0 {
		p.latency.Store(d.Nanoseconds())
		return
	}
	newVal := int64(float64(old)*(1-alpha) + float64(d.Nanoseconds())*alpha)
	p.latency.Store(newVal)
}

func (p *Server) ReportRTT(d time.Duration) {
	const alpha = 0.1
	old := p.rtt.Load()
	if old == 0 {
		p.rtt.Store(d.Nanoseconds())
		return
	}
	newVal := int64(float64(old)*(1-alpha) + float64(d.Nanoseconds())*alpha)
	p.rtt.Store(newVal)
}

// LastHealthy returns the time when the backend last became healthy.
func (p *Server) LastHealthy() time.Time {
	v := p.lastHealthy.Load()
	if v == nil {
		return time.Time{}
	}
	return v.(time.Time)
}

// IsDraining returns true if the backend is currently draining connections.
func (p *Server) IsDraining() bool {
	return p.draining.Load()
}

// SetDraining updates the draining status.
func (p *Server) SetDraining(draining bool) {
	p.draining.Store(draining)
	if draining {
		slog.Info("Backend started draining", "address", p.address)
	}
}

// ReevaluateRole triggers an immediate check of the backend role and health.
func (p *Server) Stats() BackendStats {
	st := p.core.Stat()

	// Only blocked acquisitions are timed, so the mean wait is over those.
	avgWaitDelay := time.Duration(0)
	if empty := st.EmptyAcquireCount(); empty > 0 {
		avgWaitDelay = st.AcquireDuration() / time.Duration(empty)
	}

	return BackendStats{
		MaxConns:      st.MaxConnections(),
		ActiveConns:   int32(p.checkedOut.Load()),
		IdleConns:     st.IdleConnections(),
		WaitQueueSize: int64(st.WaitingAcquires()),
		TotalRequests: p.totalRequests.Load(),
		TotalErrors:   p.totalErrors.Load(),
		AvgWaitDelay:  avgWaitDelay,
	}
}

func (p *Server) AdaptiveStatus() (isThrottled bool, reason string, currentMaxWaiters int32, activeGoroutines int32, suggestions []Suggestion) {
	if p.controller == nil {
		return false, "", 0, 0, nil
	}
	isThrottled, reason, suggestions = p.controller.Status()

	var goroutines int32
	if p.monitor != nil {
		goroutines = int32(p.monitor.Stats().NumGoroutines)
	}

	// Callers parked for a connection right now — the actual saturation signal,
	// rather than the cumulative "has been short at some point" counter.
	return isThrottled, reason, p.core.Stat().WaitingAcquires(), goroutines, suggestions
}

func (p *Server) DatabaseMetrics() *domain.DatabaseMetrics {
	return p.dbMetrics.Load()
}

func (p *Server) InstalledVersion() string {
	val := p.installedVersion.Load()
	if val == nil {
		return ""
	}
	return val.(string)
}

func (p *Server) RecommendedVersion() string {
	val := p.recommendedVersion.Load()
	if val == nil {
		return ""
	}
	return val.(string)
}

func (p *Server) AvailableVersions() []string {
	val := p.availableVersions.Load()
	if val == nil {
		return nil
	}
	return val.([]string)
}

func (p *Server) fetchAgentInfo(ctx context.Context) {
	if p.agentClient == nil {
		return
	}

	// Authorization
	if p.agentToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", p.agentToken)
	}

	resp, err := p.agentClient.GetSystemInfo(ctx, &endpoints.GetSystemInfoRequest{})
	if err != nil {
		slog.Debug("Failed to fetch agent info", "address", p.address, "error", err)
		return
	}

	if resp.DetectedVersion != "" {
		p.installedVersion.Store(resp.DetectedVersion)
	}
	if resp.RecommendedVersion != "" {
		p.recommendedVersion.Store(resp.RecommendedVersion)
	}
	if len(resp.AvailableVersions) > 0 {
		p.availableVersions.Store(resp.AvailableVersions)
	}
}

func (p *Server) IncRequests() {
	p.totalRequests.Add(1)
}

func (p *Server) IncErrors() {
	p.totalErrors.Add(1)
}

func (p *Server) ReevaluateRole() {
	select {
	case p.roleCheckChan <- struct{}{}:
	default:
	}
}

// SetHealthy updates the health status and records the time.
func (p *Server) SetHealthy(healthy bool) {
	wasHealthy := p.healthy.Swap(healthy)
	if healthy && !wasHealthy {
		p.lastHealthy.Store(time.Now())
	}
}

// Start starts background tasks for the pool.
func (p *Server) Start(ctx context.Context) {
	// No idle/min-idle tickers: the engine reaps expired connections and warms
	// back up to MinConns on its own maintenance goroutine.
	deepCheckTicker := time.NewTicker(30 * time.Second)
	metricsTicker := time.NewTicker(5 * time.Second)
	agentInfoTicker := time.NewTicker(5 * time.Minute)

	if p.controller != nil {
		go p.controller.Start(ctx)
	}

	// Initial fetch
	p.fetchAgentInfo(ctx)

	go func() {
		defer deepCheckTicker.Stop()
		defer metricsTicker.Stop()
		defer agentInfoTicker.Stop()

		consecutiveFailures := 0

		for {
			select {
			case <-deepCheckTicker.C:
				if !p.deepCheck(ctx) {
					consecutiveFailures++
					// Triage: increase frequency on failure
					deepCheckTicker.Reset(time.Second)
				} else {
					if consecutiveFailures > 0 {
						deepCheckTicker.Reset(30 * time.Second)
					}
					consecutiveFailures = 0
				}
			case <-p.roleCheckChan:
				p.deepCheck(ctx)
			case <-metricsTicker.C:
				p.reportMetrics()
			case <-agentInfoTicker.C:
				p.fetchAgentInfo(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (p *Server) reportMetrics() {
	active := float64(p.ActiveConns())
	saturation := active / float64(p.maxConns)
	observability.PoolSaturation.WithLabelValues(p.address).Set(saturation)

	// Update error rate (sliding window would be better, but simple ratio for now)
	reqs := p.totalRequests.Load()
	if reqs > 0 {
		rate := float64(p.totalErrors.Load()) / float64(reqs)
		p.errorRate.Store(math.Float64bits(rate))
	}
}

// Admin returns the administrative channel, or nil when none is configured.
func (p *Server) Admin() *AdminSession { return p.admin }

// deepCheckAdmin verifies liveness and re-detects the role over Pontus's own
// session.
//
// Preferred over the pooled path because it does not depend on a client having
// authenticated a connection first, and it runs as the operator's admin user
// rather than as whichever client last used the connection.
func (p *Server) deepCheckAdmin(ctx context.Context) bool {
	var (
		status    replicationStatus
		replaySec float64
	)
	// This used to ask only for pg_is_in_recovery() and never touch replication
	// lag, so with admin_dsn set — the recommended setup — ReplicationLag() was
	// pinned at zero for the life of the process. Every lag gate downstream
	// silently passed: the balancer's staleness filter, its cost penalty, and
	// the failover manager's choice of which replica to promote.
	if err := p.admin.QueryRow(ctx, replicationStatusQuery).
		Scan(&status.inRecovery, &replaySec, &status.caughtUp, &status.streaming); err != nil {
		p.SetHealthy(false)
		return false
	}
	p.SetHealthy(true)

	status.replayAge = time.Duration(replaySec * float64(time.Second))
	p.replicationLag.Store(status.lag().Nanoseconds())
	p.setReplicating(!status.inRecovery || status.streaming)

	if newRole := status.role(); p.Role() != newRole {
		slog.Info("Backend role changed", "address", p.address, "from", p.Role(), "to", newRole)
		p.setRole(newRole)
	}

	// A replica with no WAL receiver is not replicating, however healthy it
	// looks. Worth a line of its own: it is the state that serves stale reads
	// without erroring.
	if status.inRecovery && !status.streaming {
		slog.Warn("Replica is not streaming from a primary; its data is going stale",
			"address", p.address, "replay_age", status.lag().Round(time.Second))
	}

	return true
}

func (p *Server) deepCheck(ctx context.Context) bool {
	if p.admin.Available() {
		return p.deepCheckAdmin(ctx)
	}

	// Go through the pool rather than reaching into it: the engine owns whether
	// this reuses an idle connection or dials a fresh one, and releasing through
	// the handle keeps the accounting honest either way.
	conn, err := p.core.Acquire(ctx)
	if err != nil {
		p.SetHealthy(false)
		return false
	}
	c := conn.Conn()
	defer conn.Release()

	// 1. Basic Deep Check
	if err := p.handler.DeepCheck(ctx, c); err != nil {
		c.MarkBroken()
		p.SetHealthy(false)
		return false
	}
	p.SetHealthy(true)

	// 2. Detect Role change
	if isReadOnly, err := p.handler.IsReadOnly(ctx, c); err == nil {
		newRole := RolePrimary
		if isReadOnly {
			newRole = RoleReplica
		}
		if newRole != p.Role() {
			slog.Info("Backend role changed", "address", p.address, "old", p.Role(), "new", newRole)
			p.setRole(newRole)
		}
	}

	// 3. Check Replication Lag
	if lag, err := p.handler.GetReplicationLag(ctx, c); err == nil {
		p.replicationLag.Store(lag.Nanoseconds())
	}

	// 4. Collect detailed Database Metrics
	p.collectDatabaseMetrics(ctx, c)

	return true
}

func (p *Server) collectDatabaseMetrics(ctx context.Context, conn net.Conn) {
	metrics, err := p.handler.CollectMetrics(ctx, conn)
	if err != nil {
		slog.Debug("Failed to collect database metrics", "address", p.address, "error", err)
		return
	}

	p.dbMetrics.Store(metrics)

	observability.ActiveBackends.WithLabelValues(p.address).Set(float64(metrics.ActiveBackends))
	observability.MaxBackends.WithLabelValues(p.address).Set(float64(metrics.MaxBackends))
	observability.TransactionsCommitted.WithLabelValues(p.address).Set(float64(metrics.TransactionsCommitted))
	observability.TransactionsRolledBack.WithLabelValues(p.address).Set(float64(metrics.TransactionsRolledBack))
	observability.BlocksRead.WithLabelValues(p.address).Set(float64(metrics.BlocksRead))
	observability.BlocksHit.WithLabelValues(p.address).Set(float64(metrics.BlocksHit))
	observability.CacheHitRatio.WithLabelValues(p.address).Set(float64(metrics.CacheHitRatio))
	observability.Conflicts.WithLabelValues(p.address).Set(float64(metrics.Conflicts))
	observability.Deadlocks.WithLabelValues(p.address).Set(float64(metrics.Deadlocks))
	observability.ReplicationLagBytes.WithLabelValues(p.address).Set(float64(metrics.ReplicationLagBytes))
}

// maintainMinIdle and cleanIdle are gone: the engine warms up to MinConns and
// reaps against MaxConnIdleTime / MaxConnLifetime on its own HealthCheckPeriod
// goroutine, so duplicating either here would just race with it.

// Close shuts down the pool.
func (p *Server) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.core != nil {
		// Drains checked-out connections, then destroys the idle ones.
		p.core.Close()
	}
	if p.agentConn != nil {
		_ = p.agentConn.Close()
	}
	return nil
}

package pool

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

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
	address            string
	zone               string
	roleMu             sync.RWMutex
	role               Role
	activeConns        atomic.Int64 // Total active across all shards
	maxConns           int32
	minConns           int32
	currentMax         atomic.Int32
	shards             []*shard
	shardMask          uint32
	waitDelay          atomic.Int64 // nanoseconds (total)
	healthy            atomic.Bool
	dialTimeout        time.Duration
	maxIdleTime        time.Duration
	maxLifetime        time.Duration
	minIdle            int32
	handler            protocol.Handler
	tlsConfig          *tls.Config
	latency            atomic.Int64 // nanoseconds
	rtt                atomic.Int64 // nanoseconds
	lastHealthy        atomic.Value // time.Time
	breaker            health2.Breaker
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.RWMutex
	weight             int
	maxWaiters         int
	waitTimeout        time.Duration
	replicationLag     atomic.Int64 // nanoseconds
	draining           atomic.Bool
	roleCheckChan      chan struct{}
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
)

// NewServer creates a new Server for the given address and role.
func NewServer(address string, zone string, agentAddr string, agentToken string, role Role, weight int, maxConns int32, minIdle int32, dialTimeout time.Duration, handler protocol.Handler, tlsConfig *tls.Config, monitor *system.Monitor) (*Server, error) {
	if agentAddr == "" {
		return nil, fmt.Errorf("agent address is mandatory for backend %s", address)
	}

	ctx, cancel := context.WithCancel(context.Background())

	numShards := nextPowerOf2(uint32(runtime.GOMAXPROCS(0)))
	if numShards > 64 {
		numShards = 64
	}

	p := &Server{
		address:     address,
		zone:        zone,
		agentAddr:   agentAddr,
		agentToken:  agentToken,
		role:        role,
		weight:      weight,
		maxConns:    maxConns,
		minConns:    minIdle,
		shards:      make([]*shard, numShards),
		shardMask:   numShards - 1,
		dialTimeout: dialTimeout,
		maxIdleTime: 5 * time.Minute,
		maxLifetime: 1 * time.Hour,
		minIdle:     minIdle,
		handler:     handler,
		tlsConfig:   tlsConfig,
		breaker:     health2.NewCircuitBreaker(5, 30*time.Second),
		ctx:         ctx,
		cancel:      cancel,
		maxWaiters:  1000,
		waitTimeout: 5 * time.Second,
		monitor:     monitor,
	}

	p.controller = NewAdaptivePoolController(p, monitor)

	for i := range numShards {
		s := &shard{
			idleConns: make(chan pooledConn, max(int(maxConns)/int(numShards), 1)),
		}
		for j := range s.waiters {
			s.waiters[j] = make(chan net.Conn)
		}
		s.cond = sync.NewCond(&s.mu)
		p.shards[i] = s
	}

	p.currentMax.Store(max(minIdle, 1))
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

func nextPowerOf2(n uint32) uint32 {
	if n <= 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n++
	return n
}

func (p *Server) getShard() *shard {
	// Simple sharding by atomic counter to avoid lock contention
	idx := uint32(p.totalRequests.Load()) & p.shardMask
	return p.shards[idx]
}

// Acquire gets a connection from the pool or creates a new one.
func (p *Server) Acquire(ctx context.Context) (net.Conn, error) {
	start := time.Now()
	defer func() {
		p.waitDelay.Add(time.Since(start).Nanoseconds())
	}()

	s := p.getShard()

	// 1. Try to get a fresh idle connection from current shard
	for {
		select {
		case pc := <-s.idleConns:
			if time.Since(pc.at) > p.maxIdleTime || (p.maxLifetime > 0 && time.Since(pc.createdAt) > p.maxLifetime) {
				pc.conn.Close()
				p.releaseSlot(s)
				continue
			}
			if c, ok := pc.conn.(*Conn); ok {
				c.IncUseCount()
			}
			p.activeConns.Add(1)
			s.activeConns.Add(1)
			return pc.conn, nil
		default:
			goto tryAcquireSlot
		}
	}

tryAcquireSlot:
	// 2. Try to acquire a slot to create a new one
	if p.acquireSlot(ctx, s) {
		// Slot acquired, dial a new connection using the circuit breaker
		var conn net.Conn
		err := p.breaker.Call(ctx, func() error {
			var derr error
			conn, derr = p.dial(ctx)
			return derr
		})

		if err != nil {
			p.releaseSlot(s)
			return nil, err
		}
		if c, ok := conn.(*Conn); ok {
			c.IncUseCount()
		}
		p.activeConns.Add(1)
		s.activeConns.Add(1)
		return conn, nil
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	priority := getPriority(ctx)

	// Automatic Adaptive maxWaiters adjustment
	dynamicMaxWaiters := int64(p.maxWaiters)
	if p.monitor != nil {
		stats := p.monitor.Stats()
		// If goroutine count is high, we should be more aggressive about rejecting waiters
		// to prevent cascading failure
		if stats.NumGoroutines > 100000 {
			dynamicMaxWaiters = int64(float64(p.maxWaiters) * 0.5)
		}
	}

	if s.waitersCount[priority].Load() >= dynamicMaxWaiters/int64(len(p.shards)) {
		return nil, fmt.Errorf("wait queue full (adaptive limit %d) for priority %d", dynamicMaxWaiters, priority)
	}

	s.waitersCount[priority].Add(1)
	defer s.waitersCount[priority].Add(-1)

	// Create a sub-context with timeout if not already set or shorter
	waitCtx, cancel := context.WithTimeoutCause(ctx, p.waitTimeout, ErrPoolExhausted)
	defer cancel()

	select {
	case pc := <-s.idleConns:
		if c, ok := pc.conn.(*Conn); ok {
			c.IncUseCount()
		}
		p.activeConns.Add(1)
		s.activeConns.Add(1)
		return pc.conn, nil
	case <-waitCtx.Done():
		return nil, context.Cause(waitCtx)
	case conn := <-s.waiters[priority]:
		if c, ok := conn.(*Conn); ok {
			c.IncUseCount()
		}
		p.activeConns.Add(1)
		s.activeConns.Add(1)
		return conn, nil
	}
}

func (p *Server) acquireSlot(ctx context.Context, s *shard) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		if int32(p.activeConns.Load()) < p.currentMax.Load() {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		default:
			return false
		}
	}
}

func (p *Server) releaseSlot(s *shard) {
	s.cond.Signal()
}

// ReportResult informs the pool about the success or failure of a request,
// allowing it to adjust its capacity (AIMD).
func (p *Server) ReportResult(err error) {
	if err != nil {
		// Multiplicative Decrease
		p.mu.Lock()
		curr := p.currentMax.Load()
		newMax := int32(float64(curr) * 0.5)
		if newMax < max(p.minConns, 1) {
			newMax = max(p.minConns, 1)
		}
		p.currentMax.Store(newMax)
		p.mu.Unlock()
		return
	}

	// Additive Increase
	// Only increase if we've reached the current limit
	if int32(p.activeConns.Load()) >= p.currentMax.Load() {
		p.mu.Lock()
		curr := p.currentMax.Load()
		if curr < p.maxConns {
			p.currentMax.Store(curr + 1)
			for _, s := range p.shards {
				s.cond.Broadcast()
			}
		}
		p.mu.Unlock()
	}
}

func (p *Server) AgentClient() service.AgentServiceClient {
	return p.agentClient
}

func (p *Server) AgentToken() string {
	return p.agentToken
}

// Release returns a connection to the pool.
func (p *Server) Release(conn net.Conn) error {
	p.activeConns.Add(-1)

	var createdAt time.Time
	if c, ok := conn.(*Conn); ok {
		createdAt = c.CreatedAt()
	} else {
		createdAt = time.Now()
	}

	s := p.getShard()
	s.activeConns.Add(-1)
	return p.releaseWithMetadata(s, conn, createdAt)
}

func (p *Server) releaseWithMetadata(s *shard, conn net.Conn, createdAt time.Time) error {
	// If it's our wrapped Conn, use its actual createdAt
	if c, ok := conn.(*Conn); ok {
		createdAt = c.CreatedAt()
	}

	// If draining, close connection and release slot
	if p.IsDraining() {
		p.releaseSlot(s)
		if p.activeConns.Load() == 0 {
			p.SetDraining(false) // Finished draining
		}
		return conn.Close()
	}

	// Try to hand off to prioritized waiters first in the current shard
	for i := len(s.waiters) - 1; i >= 0; i-- {
		select {
		case s.waiters[i] <- conn:
			return nil
		default:
		}
	}

	select {
	case <-p.ctx.Done():
		return conn.Close()
	case s.idleConns <- pooledConn{conn: conn, at: time.Now(), createdAt: createdAt}:
		return nil
	default:
		// Pool full, close connection and release slot
		p.releaseSlot(s)
		return conn.Close()
	}
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

// ActiveConns returns the number of active connections.
func (p *Server) ActiveConns() int64 {
	return p.activeConns.Load()
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

func (p *Server) clearIdleConns() {
	for _, s := range p.shards {
		n := len(s.idleConns)
		for range n {
			select {
			case pc := <-s.idleConns:
				pc.conn.Close()
				p.releaseSlot(s)
			default:
				break
			}
		}
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
	var totalWaiters int64
	var totalIdle int32
	for _, s := range p.shards {
		for i := range s.waitersCount {
			totalWaiters += s.waitersCount[i].Load()
		}
		totalIdle += int32(len(s.idleConns))
	}

	avgWaitDelay := time.Duration(0)
	reqs := p.totalRequests.Load()
	if reqs > 0 {
		avgWaitDelay = time.Duration(p.waitDelay.Load() / reqs)
	}

	return BackendStats{
		MaxConns:      p.maxConns,
		ActiveConns:   int32(p.activeConns.Load()),
		IdleConns:     totalIdle,
		WaitQueueSize: totalWaiters,
		TotalRequests: reqs,
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

	return isThrottled, reason, int32(p.maxWaiters), goroutines, suggestions
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
	idleTicker := time.NewTicker(p.maxIdleTime / 2)
	minIdleTicker := time.NewTicker(10 * time.Second)
	deepCheckTicker := time.NewTicker(30 * time.Second)
	metricsTicker := time.NewTicker(5 * time.Second)
	agentInfoTicker := time.NewTicker(5 * time.Minute)

	if p.controller != nil {
		go p.controller.Start(ctx)
	}

	// Initial fetch
	p.fetchAgentInfo(ctx)

	go func() {
		defer idleTicker.Stop()
		defer minIdleTicker.Stop()
		defer deepCheckTicker.Stop()
		defer metricsTicker.Stop()
		defer agentInfoTicker.Stop()

		consecutiveFailures := 0

		for {
			select {
			case <-idleTicker.C:
				p.cleanIdle()
			case <-minIdleTicker.C:
				p.maintainMinIdle(ctx)
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

func (p *Server) deepCheck(ctx context.Context) bool {
	var conn net.Conn
	var pc pooledConn
	var fromPool bool
	var s *shard

	// Try to pick one idle connection from a random shard
	s = p.getShard()
	select {
	case pc = <-s.idleConns:
		conn = pc.conn
		fromPool = true
	default:
		// Dial a new one if none idle
		var err error
		conn, err = p.dial(ctx)
		if err != nil {
			p.SetHealthy(false)
			return false
		}
		defer conn.Close()
	}

	if fromPool {
		defer p.releaseWithMetadata(s, conn, pc.createdAt)
	}

	// 1. Basic Deep Check
	if err := p.handler.DeepCheck(ctx, conn); err != nil {
		if fromPool {
			conn.Close()
			p.releaseSlot(s)
		}
		p.SetHealthy(false)
		return false
	}
	p.SetHealthy(true)

	// 2. Detect Role change
	if isReadOnly, err := p.handler.IsReadOnly(ctx, conn); err == nil {
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
	if lag, err := p.handler.GetReplicationLag(ctx, conn); err == nil {
		p.replicationLag.Store(lag.Nanoseconds())
	}

	// 4. Collect detailed Database Metrics
	p.collectDatabaseMetrics(ctx, conn)

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

func (p *Server) maintainMinIdle(ctx context.Context) {
	if p.minIdle <= 0 {
		return
	}

	totalIdle := int32(0)
	for _, s := range p.shards {
		totalIdle += int32(len(s.idleConns))
	}

	if totalIdle >= p.minIdle {
		return
	}

	needed := p.minIdle - totalIdle
	for range needed {
		// Try to acquire a slot and dial in a random shard
		s := p.getShard()
		if p.acquireSlot(ctx, s) {
			go func(sh *shard) {
				conn, err := p.dial(ctx)
				if err != nil {
					p.releaseSlot(sh)
					return
				}
				// Put in pool
				if err := p.releaseWithMetadata(sh, conn, time.Now()); err != nil {
					// Connection closed by releaseWithMetadata if pool full
				}
			}(s)
		} else {
			// No slots available
			return
		}
	}
}

func (p *Server) cleanIdle() {
	for _, s := range p.shards {
		n := len(s.idleConns)
		for range n {
			select {
			case pc := <-s.idleConns:
				if time.Since(pc.at) > p.maxIdleTime {
					pc.conn.Close()
					p.releaseSlot(s)
				} else {
					s.idleConns <- pc
				}
			default:
				break
			}
		}
	}
}

// Close shuts down the pool.
func (p *Server) Close() error {
	if p.cancel != nil {
		p.cancel()
	}

	// Close all idle connections in all shards
	for _, s := range p.shards {
		close(s.idleConns)
		for pc := range s.idleConns {
			pc.conn.Close()
		}
	}

	return nil
}

func (p *Server) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: p.dialTimeout}
	var conn net.Conn
	var err error
	if p.tlsConfig != nil {
		conn, err = tls.DialWithDialer(&d, "tcp", p.address, p.tlsConfig)
	} else {
		conn, err = d.DialContext(ctx, "tcp", p.address)
	}

	if err != nil {
		return nil, err
	}
	return NewConn(conn), nil
}

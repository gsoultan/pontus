package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/pontus/internal/system"
	"github.com/gsoultan/pontus/pkg/buffer"
	"github.com/gsoultan/pontus/pkg/config"
	observability2 "github.com/gsoultan/pontus/pkg/observability"
	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/cache"
	"github.com/gsoultan/pontus/server/internal/credentials"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
	"github.com/gsoultan/pontus/server/proxy/middleware"
	"golang.org/x/time/rate"
)

// runtimeConfig is the hot-reloadable state the query path reads.
//
// It is swapped as one immutable value rather than mutated field by field:
// handleClient reads it on every request and cannot take a lock without
// holding it across connection I/O.
type runtimeConfig struct {
	chain   middleware.Chain
	pooling poolingMode

	// slowQuery is the threshold above which a query is logged individually.
	slowQuery time.Duration

	// localZone is the zone this proxy runs in. The balancer's cost function
	// applies RemoteZonePenalty when it differs from a backend's zone, but only
	// if the caller says where it is — every Hint left it empty, so local_zone
	// was configured, merged, and never affected routing.
	localZone string
}

// Gateway implements the proxy.Provider interface.
type Gateway struct {
	handler        protocol.Handler
	balancer       balancer2.Balancer
	orchestrator   FailoverOrchestrator
	wg             sync.WaitGroup
	limiter        *rate.Limiter
	tenantLimiters *middleware.TenantLimiters
	cacheConfig    *config.Cache
	cacheManager   *cache.Manager
	failoverMu     sync.RWMutex
	inFailover     atomic.Bool
	pauseCond      *sync.Cond
	collapserMu    sync.Mutex
	inFlight       map[string]*inflightCall
	queryTimeout   time.Duration
	chain          middleware.Chain
	runtime        atomic.Pointer[runtimeConfig]

	// streamCtx is cancelled to end every replication stream at once. Guarded
	// by streamCtxMu because failover replaces it rather than reusing a
	// cancelled one.
	streamCtxMu  sync.Mutex
	streamCtx    context.Context
	streamCancel context.CancelFunc

	// streams tracks live CDC consumers. Set by the control plane; nil means
	// replication is refused rather than carried unaccounted.
	streams        atomic.Pointer[StreamRegistry]
	configMu       sync.RWMutex
	config         *config.Options
	shadowBackends []pool2.Backend
	monitor        *system.Monitor
	backendTLS     *tls.Config

	// credentials is set when Pontus authenticates clients itself. Nil means
	// passthrough: the client's own exchange is relayed to one backend.
	credentials credentials.Store

	// ctx bounds gateway-owned background work (the cache janitor) so it stops
	// with the gateway rather than outliving it.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewGateway creates a new Gateway.
func NewGateway(h protocol.Handler, b balancer2.Balancer, orch FailoverOrchestrator, cfg *config.Options, backendTLS *tls.Config) *Gateway {
	m := system.NewMonitor()
	m.Start(5 * time.Second)

	g := new(Gateway)
	g.ctx, g.cancel = context.WithCancel(context.Background())
	// Built once and never replaced. reconfigure() used to rebuild it on every
	// hot reload, which both raced with handleClient reading it unlocked and
	// stranded any goroutine already parked in pauseCond.Wait() — Broadcast on
	// the new cond cannot wake a waiter on the old one.
	g.pauseCond = sync.NewCond(g.failoverMu.RLocker())
	g.handler = h
	g.balancer = b
	g.orchestrator = orch
	g.inFlight = make(map[string]*inflightCall)
	g.queryTimeout = 30 * time.Second // Default query timeout
	g.config = cfg
	g.monitor = m
	g.backendTLS = backendTLS

	g.reconfigure(cfg)
	return g
}

func (g *Gateway) reconfigure(cfg *config.Options) {
	if cfg.QueryTimeout > 0 {
		g.queryTimeout = cfg.QueryTimeout
	}
	// How stale a replica may be before reads stop going to it. Applied on
	// reload as well as at startup: an operator raising this during a
	// replication backlog should not have to restart the proxy.
	fo := cfg.FailoverOptions()
	balancer2.SetMaxReplicaLag(fo.MaxReplicaLag)
	// Whether a replica that lost its WAL receiver is pulled from the read pool,
	// and how long it must stream cleanly before it is trusted again.
	pool2.SetAutoReattach(*fo.AutoReattach, fo.AutoReattachInterval)
	if cfg.RateLimit != nil && cfg.RateLimit.Enabled {
		g.limiter = rate.NewLimiter(rate.Limit(cfg.RateLimit.RPS), cfg.RateLimit.Burst)
		// Per-tenant limiters share the configured rate and live in a bounded,
		// self-evicting map. The key is a client-supplied username, so an
		// unbounded map is a remote OOM rather than a rate limit.
		g.tenantLimiters = middleware.NewTenantLimiters(
			rate.Limit(cfg.RateLimit.RPS), cfg.RateLimit.Burst, middleware.DefaultMaxTenants)
	} else {
		g.limiter = nil
		g.tenantLimiters = nil
	}
	if cfg.Cache != nil && cfg.Cache.Enabled {
		g.cacheConfig = cfg.Cache
		if g.cacheManager == nil {
			// MaxSize was parsed and never read, so the map grew without bound
			// keyed by client-supplied query text. The janitor is what reclaims
			// entries no write happens to invalidate.
			g.cacheManager = cache.NewManagerWithSize(cfg.Cache.MaxSize)
			g.cacheManager.StartJanitor(time.Minute, g.ctx.Done())
		}
	} else {
		g.cacheConfig = nil
	}

	// Reconfigure shadow backends
	if len(cfg.ShadowBackends) > 0 {
		// In a real scenario, we'd reuse existing ones, but for now we'll recreate
		// if they changed. For simplicity in this implementation, we just clear and re-add.
		for _, b := range g.shadowBackends {
			b.Close()
		}
		g.shadowBackends = nil
		for _, bcfg := range cfg.ShadowBackends {
			agentAddr := bcfg.AgentAddr
			backendAddr := bcfg.Addr
			if agentAddr == "" && backendAddr != "" {
				host, _, _ := net.SplitHostPort(backendAddr)
				agentAddr = net.JoinHostPort(host, "9091")
			} else if agentAddr != "" && backendAddr == "" {
				host, _, _ := net.SplitHostPort(agentAddr)
				backendAddr = net.JoinHostPort(host, "5432")
			}

			p, err := pool2.NewServer(backendAddr, bcfg.Zone, agentAddr, bcfg.AgentToken, pool2.Role(bcfg.Role), bcfg.Weight, cfg.MaxConns, cfg.MinIdle, cfg.DialTimeout, g.handler, g.backendTLS, g.monitor, bcfg.AdminDSN)
			if err != nil {
				slog.Error("Failed to create shadow backend", "address", backendAddr, "error", err)
				continue
			}
			p.Start(context.Background())
			g.shadowBackends = append(g.shadowBackends, p)
		}
	} else {
		for _, b := range g.shadowBackends {
			b.Close()
		}
		g.shadowBackends = nil
	}

	// Initialize middleware chain
	g.chain = middleware.Chain{
		middleware.NewRateLimit(g.limiter, g.tenantLimiters),
		middleware.NewCache(g.cacheManager, g.cacheConfig, g.handler),
	}

	// Publish atomically for the query path.
	g.runtime.Store(&runtimeConfig{chain: g.chain, pooling: parsePoolingMode(cfg.PoolingMode), localZone: cfg.LocalZone, slowQuery: slowQueryThreshold(cfg)})
}

// current returns the active runtime configuration. Never nil after
// NewGateway, which calls reconfigure before returning.
// defaultSlowQueryThreshold is the point above which a query is worth a line of
// its own. Chosen to be well clear of ordinary OLTP work while still catching
// the queries an operator would want to see named.
const defaultSlowQueryThreshold = 200 * time.Millisecond

func slowQueryThreshold(cfg *config.Options) time.Duration {
	if cfg != nil && cfg.SlowQueryThreshold > 0 {
		return cfg.SlowQueryThreshold
	}
	return defaultSlowQueryThreshold
}

func (g *Gateway) current() *runtimeConfig {
	if rc := g.runtime.Load(); rc != nil {
		return rc
	}
	return &runtimeConfig{}
}

// UpdateConfig updates the gateway configuration at runtime.
func (g *Gateway) UpdateConfig(cfg *config.Options) {
	g.configMu.Lock()
	defer g.configMu.Unlock()
	g.config.Merge(cfg)
	g.reconfigure(g.config)
	slog.Info("Gateway configuration updated")
}

// Serve starts accepting connections.
func (g *Gateway) Serve(ctx context.Context, ln net.Listener) error {
	slog.Info("Gateway starting", "addr", ln.Addr().String())
	for {
		client, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				slog.Error("Accept error", "error", err)
				continue
			}
		}

		g.wg.Go(func() {
			g.handleClient(ctx, client)
		})
	}
}

func (m *Gateway) executeRequest(ctx context.Context, s *middleware.Session) error {
	start := time.Now()
	// 1. Request Collapsing (Thundering Herd Protection)
	// Only for simple SELECTs (idempotent reads) and not in transaction
	canCollapse := s.QueryInfo.ReadOnly && !s.QueryInfo.InTransaction && s.State.TxState == protocol.StateIdle
	var call *inflightCall
	if canCollapse {
		// Same rule as the cache: collapsing two requests means one client is
		// served the other's bytes, so the key has to include who asked.
		key := middleware.CacheKey(s)
		m.collapserMu.Lock()
		if c, ok := m.inFlight[key]; ok {
			m.collapserMu.Unlock()
			c.wg.Wait()
			if c.err == nil {
				_, err := s.Client.Write(c.data)
				return err
			}
			// If first call failed, we fall through and retry
		} else {
			call = &inflightCall{}
			call.wg.Add(1)
			m.inFlight[key] = call
			m.collapserMu.Unlock()

			defer func() {
				m.collapserMu.Lock()
				delete(m.inFlight, key)
				m.collapserMu.Unlock()
				call.wg.Done()
			}()
		}
	}

	// 2. Intelligent Consistency: Wait for LSN if reading from replica
	if s.QueryInfo.ReadOnly && s.State.LastLSN != "" {
		// If we haven't acquired a server yet, we'll do it in acquireBackend
		// But acquireBackend needs to know about the LSN.
	}

	// Ensure we have a server connection
	if s.Server == nil {
		backend, server, err := m.acquireForSession(ctx, balancer2.Hint{
			CallerZone: m.current().localZone,
			ReadOnly:   s.QueryInfo.ReadOnly,
			Key:        s.RemoteAddr,
		}, s.HomeBackend, s.State)
		if err != nil {
			return err
		}

		// If it's a replica, wait for LSN consistency
		if s.QueryInfo.ReadOnly && s.State.LastLSN != "" && backend.Role() == pool2.RoleReplica {
			if err := m.handler.WaitLSN(ctx, server, s.State.LastLSN); err != nil {
				slog.Warn("LSN wait failed or timed out, routing to primary", "error", err)
				backend.Release(server)
				// Retry with primary
				backend, server, err = m.acquireForSession(ctx, balancer2.Hint{
					CallerZone: m.current().localZone,
					ReadOnly:   false,
					Key:        s.RemoteAddr,
				}, s.HomeBackend, s.State)
				if err != nil {
					return err
				}
			}
		}

		// The gap between read-intent requests and reads actually served by a
		// replica is the health of the read/write split in one number, and it is
		// not visible from connection counts or latency.
		intent := "write"
		if s.QueryInfo.ReadOnly {
			intent = "read"
		}
		observability2.RoutedRequests.
			WithLabelValues(backend.Address(), intent, string(backend.Role())).Inc()

		s.Backend = backend
		s.Server = server
		s.ShouldReplay = true
	}

	// 3. Traffic Shadowing (Mirroring)
	if len(m.shadowBackends) > 0 {
		// Copy: s.Data is a view into the one per-session buffer that the read
		// loop overwrites on the next client message, so a goroutine reading it
		// later mirrors whatever the session moved on to.
		mirrored := bytes.Clone(s.Data)
		m.wg.Go(func() {
			m.mirrorRequest(context.Background(), mirrored, s.State)
		})
	}

	// Replay session state if needed
	if s.ShouldReplay {
		if err := m.handler.ReplaySessionState(ctx, s.Server, s.State); err != nil {
			slog.Warn("Failed to replay session state", "client", s.RemoteAddr, "error", err)
		}
		if err := m.handler.ReplayPreparedStatements(ctx, s.Server, s.State); err != nil {
			slog.Warn("Failed to replay prepared statements", "client", s.RemoteAddr, "error", err)
		}
	}

	if _, err := s.Server.Write(s.Data); err != nil {
		s.Backend.Release(s.Server)
		s.Server = nil
		return err
	}

	// Track only what the backend has actually been told, and only after the
	// been admitted. Recording the statement against this specific
	// connection is what stops a later replay re-parsing a name it already has.
	m.handler.TrackSessionState(s.State, s.Data)
	m.handler.TrackPreparedStatement(s.State, s.Data)
	if name := protocol.ParseStatementName(s.Data); name != "" {
		if holder, ok := s.Server.(protocol.StatementHolder); ok {
			holder.AddStatement(name)
		}
	}

	// One buffer, shared.
	//
	// The collapser and the result cache both want the response bytes, and the
	// conditions that enable collapsing — a read-only statement outside a
	// transaction — are exactly the ones the cache stores under. Giving the
	// collapser its own buffer left ResponseCapture empty, so the cache's Set
	// never ran and every lookup missed: the cache was enabled, consulted on
	// every query, and structurally incapable of holding anything.
	var capture *bytes.Buffer
	switch {
	case s.ResponseCapture != nil:
		capture = s.ResponseCapture
	case call != nil:
		capture = new(bytes.Buffer)
	}

	state, isReadOnlyErr, rtt, err := m.proxyResponse(s.Client, s.Server, s.Buffer, capture,
		s.QueryInfo.ReadOnly, m.handler.ResponseEndFor(s.Data))
	if call != nil {
		call.data = capture.Bytes()
		call.err = err
	}
	if rtt > 0 {
		s.Backend.ReportRTT(rtt)
	}
	s.Backend.ReportResult(err)

	if isReadOnlyErr {
		// If we got a read-only error on primary, it might have been promoted/demoted
		s.Backend.ReevaluateRole()
	}

	// Update session state
	s.State.TxState = state

	// 2. Intelligent Consistency: Capture LSN after a write
	if !s.QueryInfo.ReadOnly && err == nil {
		if lsn, lerr := m.handler.GetCurrentLSN(ctx, s.Server); lerr == nil {
			s.State.LastLSN = lsn
		}
	}

	// Log query execution for Live Traffic Stream
	duration := time.Since(start)
	status := "success"
	if err != nil {
		status = "error"
	}
	// Feed the tracker. Without this call nothing on the query path is ever
	// observed: total requests, error rate, RPS and Top Queries all stayed at
	// zero in a real deployment while the dashboard rendered them as fact.
	// Per-backend counters. Recorded here rather than at the chain boundary
	// because they answer "how is the balancer distributing load", and a
	// request served from cache never reached a backend.
	if s.Backend != nil {
		s.Backend.IncRequests()
		if err != nil {
			s.Backend.IncErrors()
		}
	}

	// Log the queries worth reading, not every query.
	//
	// An INFO line per query carrying its full SQL text was the dominant log
	// volume on a busy proxy — it drowned the events an operator actually
	// looks for, inflated the log store, and put unbounded client-supplied
	// text into a per-request record. Failures and slow queries still log
	// individually; the rest are counted by the tracker and available at debug
	// level, with Top Queries as the aggregate view.
	level := slog.LevelDebug
	switch {
	case err != nil:
		level = slog.LevelError
	case duration >= m.current().slowQuery:
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "Query executed",
		"query", s.Normalized,
		"duration_ms", duration.Milliseconds(),
		"backend", s.Backend.Address(),
		"status", status,
		"client", s.RemoteAddr,
	)

	// Return the connection according to the configured pooling mode.
	//
	// This site released on an idle transaction unconditionally, which meant
	// session pooling never held anything: the mode was honoured in the
	// transaction loop and ignored here, and this runs first. Testing the
	// predicate in isolation proved it correct and proved nothing about it
	// being the only thing that releases.
	if s.Server != nil &&
		m.current().pooling.shouldReleaseAt(state, m.handler.IsPinned(s.State)) {
		if m.resetOnRelease() {
			releaseToPool(s.Backend, s.Server)
		} else {
			s.Backend.Release(s.Server)
		}
		s.Server = nil
	}

	// Report the backend's failure.
	//
	// This returned nil unconditionally: a backend that went away was logged
	// at error level and then reported to the caller as success. Nothing was
	// written to the client and the session loop went back to waiting for the
	// next message, so the client sat until its own deadline expired against a
	// proxy that had already noticed — measured at 40s for a failure the proxy
	// detected in under a millisecond.
	//
	// isReadOnlyErr is the one exception: a write that landed on a demoted
	// primary is retried elsewhere rather than failed, and that path has
	// already handled it.
	if err != nil && !isReadOnlyErr {
		return err
	}

	return nil
}

func (g *Gateway) mirrorRequest(ctx context.Context, data []byte, state *protocol.SessionState) {
	// Send to all shadow backends
	for _, b := range g.shadowBackends {
		go func(backend pool2.Backend) {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			conn, err := backend.Acquire(sctx)
			if err != nil {
				return
			}
			defer backend.Release(conn)

			// Mirroring is best-effort, we don't care about the result
			if err := g.handler.ReplaySessionState(sctx, conn, state); err != nil {
				return
			}
			_, _ = conn.Write(data)

			// We don't read the response to keep it fast, or we could read and discard
			buf := buffer.Get()
			defer buffer.Put(buf)
			_, _ = conn.Read(buf)
		}(b)
	}
}

func (g *Gateway) handleClient(ctx context.Context, client net.Conn) {
	ctx, span := observability2.StartSpan(ctx, "handleClient")
	defer span.End()
	defer client.Close()
	remoteAddr := client.RemoteAddr().String()
	sessionState := &protocol.SessionState{}

	// Allocate buffer once for the whole session
	buf := buffer.Get()
	defer buffer.Put(buf)

	backend, server, err := g.openSession(ctx, client, sessionState, remoteAddr)
	if err != nil {
		// A replication stream is not a failure: it needs the node holding its
		// slot rather than the balanced one, so it is carried on its own path.
		if errors.Is(err, protocol.ErrReplicationRequested) {
			g.handleReplication(ctx, client, sessionState, remoteAddr)
			return
		}
		slog.Error("Handshake error", "client", remoteAddr, "error", err)
		return
	}

	// The startup exchange completed, so this connection can carry queries and
	// may be recycled. Until it is marked, the pool destroys it on release
	// rather than handing an unusable socket to the next caller.
	if c, ok := server.(interface{ MarkReady() }); ok {
		c.MarkReady()
	}

	session := &middleware.Session{
		Client:     client,
		RemoteAddr: remoteAddr,
		State:      sessionState,
		Backend:    backend,
		Server:     server,
		Buffer:     buf,
		// The one backend holding a connection this session has spoken on.
		// Acquisition falls back here when the balancer picks a backend whose
		// connections never carried this session's handshake.
		HomeBackend: backend,
	}

	// Give the startup connection back before the first query.
	//
	// It was acquired to complete the client's startup, with a write hint
	// because nothing was known about the session yet — so keeping it meant
	// every session's first statement ran on the primary whatever it asked for,
	// and a client that issues one read per connection never touched a replica
	// at all. Releasing it lets that first statement route on its own hint.
	//
	// Only when Pontus can authenticate a replacement. Under passthrough this
	// connection is the session's only way to reach a backend.
	if g.credentials != nil && g.current().pooling.shouldRelease(sessionState, false) {
		releaseToPool(session.Backend, session.Server)
		session.Server = nil
		session.Backend = nil
	}

	// Transaction loop
	for {
		// Poison-pill protection: Apply query timeout
		qctx, qcancel := context.WithTimeout(ctx, g.queryTimeout)

		// Read from client
		n, err := client.Read(buf)
		if err != nil {
			qcancel()
			releaseToPool(session.Backend, session.Server)
			if n > 0 || err != net.ErrClosed {
				slog.Debug("Client read error or close", "client", remoteAddr, "error", err)
			}
			return
		}

		session.Data = buf[:n]

		// A client saying goodbye must not take the backend connection with it —
		// but only when Pontus can authenticate the next borrower itself.
		//
		// Under passthrough the connection carries the client's own startup
		// exchange and there is no way to re-run one for somebody else: the next
		// client's startup packet would arrive on a connection already past that
		// phase. So passthrough forwards the goodbye and gives the connection up,
		// which is why it cannot pool. Reuse is a property of Pontus-side
		// authentication, not of the release path.
		if g.credentials != nil && g.handler.IsTerminate(session.Data) {
			qcancel()
			releaseToPool(session.Backend, session.Server)
			session.Server = nil
			return
		}

		// Session state is tracked in executeRequest, once the message has actually
		// been written to a backend.
		// onto the very connection about to parse it.

		session.Normalized = g.handler.NormalizeQuery(session.Data)
		session.QueryInfo = g.handler.ClassifyQuery(session.Data)

		// Execute through middleware chain
		rc := g.current()
		queryStart := time.Now()

		// Statement pooling cannot hold a connection across statements, so a
		// transaction spanning them would run on different backends. Refuse it
		// rather than execute half of it somewhere else.
		if rc.pooling.rejectsTransactions() && session.QueryInfo.InTransaction {
			slog.Warn("Refused transaction under statement pooling",
				"client", remoteAddr, "user", session.State.User)
			if err := protocol.WritePostgresError(client, "0A000",
				"statement pooling does not support transactions; use transaction or session pooling"); err != nil {
				slog.Debug("Failed to report statement-pooling refusal", "client", remoteAddr, "error", err)
			}
			qcancel()
			continue
		}

		chainErr := rc.chain.Handle(qctx, session, g.executeRequest)

		// Counted at the chain boundary, so a request served from cache still
		// counts as a request served. Recording inside executeRequest missed
		// every cache hit, which under-reported throughput by exactly the
		// amount the cache was helping.
		observability2.DefaultTracker.Record(session.Normalized, time.Since(queryStart), chainErr)

		if err := chainErr; err != nil {
			slog.Error("Request failed", "client", remoteAddr, "error", err)
			qcancel()
			if session.Server != nil {
				session.Backend.Release(session.Server)
				session.Server = nil
			}
			return
		}

		// Return the connection according to the configured pooling mode.
		// Session mode holds it until the client disconnects; transaction and
		// statement modes wait for the transaction to close. A pinned session
		// is never released, whatever the mode.
		if session.Server != nil &&
			rc.pooling.shouldRelease(session.State, g.handler.IsPinned(session.State)) {
			if g.resetOnRelease() {
				releaseToPool(session.Backend, session.Server)
			} else {
				session.Backend.Release(session.Server)
			}
			session.Server = nil
			session.Backend = nil
		}
		qcancel()
	}
}

func (g *Gateway) acquireBackend(ctx context.Context, hint balancer2.Hint) (pool2.Backend, net.Conn, error) {
	var lastErr error
	for i := range 3 {
		// If in failover, wait for resolution — but no longer than the caller
		// is prepared to wait.
		if !hint.ReadOnly {
			if err := g.waitWhilePaused(ctx); err != nil {
				return nil, nil, err
			}
		}

		backend, err := g.balancer.Next(ctx, hint)
		if err == nil {
			conn, aerr := backend.Acquire(ctx)
			if aerr == nil {
				return backend, conn, nil
			}
			lastErr = aerr
		} else {
			lastErr = err
		}

		// If we can't find a primary, trigger failover wait
		if !hint.ReadOnly && lastErr != nil {
			if errors.Is(lastErr, balancer2.ErrNoHealthyBackends) || i == 2 {
				g.triggerFailover()
				// After triggering failover, if it's the first or second attempt,
				// the loop will continue and the next iteration will hit the wait block.
			}
		}

		if i < 2 {
			timer := time.NewTimer(time.Duration(i+1) * 100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, nil, ctx.Err()
			}
		}
	}
	return nil, nil, lastErr
}

// waitWhilePaused blocks writes until a failover resolves, or until the
// caller's context expires.
//
// The wait used to be an unbounded sync.Cond, so a client waited for the
// failover goroutine to give up rather than for its own query timeout. With a
// single backend that is the common outage: there is no replica to promote,
// the failover cannot succeed, and every query stalled for thirty seconds
// before failing anyway. Pausing briefly to ride out a promotion is the point;
// outlasting the caller's deadline is not.
func (g *Gateway) waitWhilePaused(ctx context.Context) error {
	if !g.inFailover.Load() {
		return nil
	}

	// sync.Cond has no context, so the context is given a way to wake the
	// waiter: on cancellation this broadcast returns Wait, the loop re-checks,
	// and the caller gets its own error rather than someone else's timeout.
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			g.pauseCond.Broadcast()
		case <-done:
		}
	}()

	g.failoverMu.RLock()
	defer g.failoverMu.RUnlock()

	for g.inFailover.Load() {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.pauseCond.Wait()
	}
	return nil
}

func (g *Gateway) triggerFailover() {
	if g.inFailover.CompareAndSwap(false, true) {
		slog.Warn("Primary lost, entering failover wait mode")

		// Replication slots do not exist on the promoted node, so a stream
		// cannot follow the write path. Ending it is the honest outcome.
		g.terminateStreams("primary lost, failover started")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			if g.orchestrator != nil {
				if err := g.orchestrator.TriggerFailover(ctx); err != nil {
					slog.Error("Failover orchestration failed", "error", err)
				}
			}

			// Wait up to 30 seconds for a new primary to appear if orchestration didn't finish or failed.
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			timeout := time.After(30 * time.Second)

			for {
				select {
				case <-ticker.C:
					// Check if a primary is available
					backend, err := g.balancer.Next(context.Background(),
						balancer2.Hint{ReadOnly: false, CallerZone: g.current().localZone})
					if err == nil && backend.IsHealthy() && backend.Role() == pool2.RolePrimary {
						slog.Info("New primary detected, resolving failover", "address", backend.Address())
						g.inFailover.Store(false)
						g.pauseCond.Broadcast()
						return
					}
				case <-timeout:
					slog.Error("Failover wait timed out")
					g.inFailover.Store(false)
					g.pauseCond.Broadcast()
					return
				}
			}
		}()
	}
}

// Stop waits for all active connections to close.
func (g *Gateway) Stop(ctx context.Context) error {
	slog.Info("Gateway stopping, waiting for active connections")
	if g.cancel != nil {
		g.cancel()
	}
	if g.monitor != nil {
		g.monitor.Stop()
	}
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CacheManager returns the internal cache manager.
// Handler returns the protocol handler this gateway speaks. The control plane
// needs it for protocol-specific administration such as creating a logical
// replication slot.
func (g *Gateway) Handler() protocol.Handler { return g.handler }

// LocalZone reports the zone this proxy runs in, so the dashboard can show
// which backends the balancer treats as remote.
func (g *Gateway) LocalZone() string { return g.current().localZone }

func (g *Gateway) CacheManager() *cache.Manager {
	return g.cacheManager
}

func (g *Gateway) proxyResponse(client, server net.Conn, buf []byte, capture *bytes.Buffer, readOnly bool, end protocol.ResponseEnd) (protocol.TransactionState, bool, time.Duration, error) {
	// Fast Path: a read-only query with nothing to capture can be steered straight through.
	if readOnly && capture == nil {
		return g.proxyResponseFastPath(client, server, buf, end)
	}

	return g.proxyResponseWithCapture(client, server, buf, capture, end)
}

func (g *Gateway) proxyResponseFastPath(client, server net.Conn, buf []byte, end protocol.ResponseEnd) (protocol.TransactionState, bool, time.Duration, error) {
	start := time.Now()
	firstByte := true
	var rtt time.Duration
	scan := protocol.NewReplyScanner(end)

	for {
		n, err := server.Read(buf)
		if n > 0 {
			if firstByte {
				rtt = time.Since(start)
				firstByte = false
			}

			if _, werr := client.Write(buf[:n]); werr != nil {
				return protocol.StateError, false, rtt, werr
			}

			if done, state := scan.Feed(buf[:n]); done {
				return midSequenceState(state, end), false, rtt, nil
			}
		}
		if err != nil {
			return protocol.StateError, false, rtt, err
		}
	}
}

// midSequenceState keeps a connection pinned when the client is mid-batch.
//
// A Flush-terminated request leaves the extended-protocol sequence open — the
// client still owes a Sync — so reporting Idle here would return the connection
// to the pool between a Parse and its Bind, and hand the next statement to a
// backend that never saw the statement being bound.
func midSequenceState(state protocol.TransactionState, end protocol.ResponseEnd) protocol.TransactionState {
	if !end.OnReadyForQuery {
		return protocol.StateInTransaction
	}
	return state
}

func (g *Gateway) proxyResponseWithCapture(client, server net.Conn, buf []byte, capture *bytes.Buffer, end protocol.ResponseEnd) (protocol.TransactionState, bool, time.Duration, error) {
	isReadOnlyErr := false
	var rtt time.Duration
	start := time.Now()
	firstByte := true
	scan := protocol.NewReplyScanner(end)
	for {
		n, err := server.Read(buf)
		if n > 0 {
			if firstByte {
				rtt = time.Since(start)
				firstByte = false
			}
			if capture != nil {
				capture.Write(buf[:n])
			}
			if _, werr := client.Write(buf[:n]); werr != nil {
				return protocol.StateError, false, rtt, werr
			}

			if g.handler.IsReadOnlyError(buf[:n]) {
				isReadOnlyErr = true
			}

			// The reply ends at ReadyForQuery when the client sent a Sync, and
			// at the answer to its last message when it sent a Flush. Waiting
			// for ReadyForQuery either way blocks forever on the second.
			if done, state := scan.Feed(buf[:n]); done {
				return midSequenceState(state, end), isReadOnlyErr, rtt, nil
			}
		}
		if err != nil {
			return protocol.StateError, isReadOnlyErr, rtt, err
		}
	}
}

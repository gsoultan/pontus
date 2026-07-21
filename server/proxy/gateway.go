package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gsoultan/pontus/internal/system"
	"github.com/gsoultan/pontus/pkg/buffer"
	"github.com/gsoultan/pontus/pkg/config"
	observability2 "github.com/gsoultan/pontus/pkg/observability"
	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/cache"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
	"github.com/gsoultan/pontus/server/proxy/middleware"
	"golang.org/x/time/rate"
)

// Gateway implements the proxy.Provider interface.
type Gateway struct {
	handler        protocol.Handler
	balancer       balancer2.Balancer
	orchestrator   FailoverOrchestrator
	wg             sync.WaitGroup
	limiter        *rate.Limiter
	tenantLimiters sync.Map // string -> *rate.Limiter
	fwConfig       *config.Firewall
	fwPatterns     []*regexp.Regexp
	cacheConfig    *config.Cache
	cacheManager   *cache.Manager
	failoverMu     sync.RWMutex
	inFailover     atomic.Bool
	pauseCond      *sync.Cond
	collapserMu    sync.Mutex
	inFlight       map[string]*inflightCall
	queryTimeout   time.Duration
	chain          middleware.Chain
	configMu       sync.RWMutex
	config         *config.Options
	shadowBackends []pool2.Backend
	monitor        *system.Monitor
	backendTLS     *tls.Config
}

// NewGateway creates a new Gateway.
func NewGateway(h protocol.Handler, b balancer2.Balancer, orch FailoverOrchestrator, cfg *config.Options, backendTLS *tls.Config) *Gateway {
	m := system.NewMonitor()
	m.Start(5 * time.Second)

	g := new(Gateway)
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
	g.pauseCond = sync.NewCond(g.failoverMu.RLocker())
	if cfg.RateLimit != nil && cfg.RateLimit.Enabled {
		g.limiter = rate.NewLimiter(rate.Limit(cfg.RateLimit.RPS), cfg.RateLimit.Burst)
	} else {
		g.limiter = nil
	}
	if cfg.Firewall != nil && cfg.Firewall.Enabled {
		g.fwConfig = cfg.Firewall
		g.fwPatterns = nil
		for _, p := range cfg.Firewall.Patterns {
			if re, err := regexp.Compile(p); err == nil {
				g.fwPatterns = append(g.fwPatterns, re)
			} else {
				slog.Error("Failed to compile firewall pattern", "pattern", p, "error", err)
			}
		}
	} else {
		g.fwConfig = nil
		g.fwPatterns = nil
	}
	if cfg.Cache != nil && cfg.Cache.Enabled {
		g.cacheConfig = cfg.Cache
		if g.cacheManager == nil {
			g.cacheManager = cache.NewManager()
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

			p, err := pool2.NewServer(backendAddr, bcfg.Zone, agentAddr, bcfg.AgentToken, pool2.Role(bcfg.Role), bcfg.Weight, cfg.MaxConns, cfg.MinIdle, cfg.DialTimeout, g.handler, g.backendTLS, g.monitor)
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
		middleware.NewRateLimit(g.limiter, &g.tenantLimiters),
		middleware.NewFirewall(g.fwConfig, g.handler),
		middleware.NewCache(g.cacheManager, g.cacheConfig, g.handler),
	}
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
		key := string(s.Data)
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
		backend, server, err := m.acquireBackend(ctx, balancer2.Hint{
			ReadOnly: s.QueryInfo.ReadOnly,
			Key:      s.RemoteAddr,
		})
		if err != nil {
			return err
		}

		// If it's a replica, wait for LSN consistency
		if s.QueryInfo.ReadOnly && s.State.LastLSN != "" && backend.Role() == pool2.RoleReplica {
			if err := m.handler.WaitLSN(ctx, server, s.State.LastLSN); err != nil {
				slog.Warn("LSN wait failed or timed out, routing to primary", "error", err)
				backend.Release(server)
				// Retry with primary
				backend, server, err = m.acquireBackend(ctx, balancer2.Hint{
					ReadOnly: false,
					Key:      s.RemoteAddr,
				})
				if err != nil {
					return err
				}
			}
		}

		s.Backend = backend
		s.Server = server
		s.ShouldReplay = true
	}

	// 3. Traffic Shadowing (Mirroring)
	if len(m.shadowBackends) > 0 {
		m.wg.Go(func() {
			m.mirrorRequest(context.Background(), s.Data, s.State)
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

	var capture *bytes.Buffer
	if call != nil {
		capture = new(bytes.Buffer)
	} else if s.ResponseCapture != nil {
		capture = s.ResponseCapture
	}

	state, isReadOnlyErr, rtt, err := m.proxyResponse(s.Client, s.Server, s.Buffer, capture, s.QueryInfo.ReadOnly)
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
	slog.Info("Query executed",
		"query", s.Normalized,
		"duration_ms", duration.Milliseconds(),
		"backend", s.Backend.Address(),
		"status", status,
		"client", s.RemoteAddr,
	)

	// If the transaction is idle, we can release the server connection back to the pool.
	if state == protocol.StateIdle {
		s.Backend.Release(s.Server)
		s.Server = nil
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

	// For the initial handshake, we need a backend.
	backend, server, err := g.acquireBackend(ctx, balancer2.Hint{
		ReadOnly: false,
		Key:      remoteAddr,
	})
	if err != nil {
		slog.Error("Failed to acquire backend for handshake", "client", remoteAddr, "error", err)
		return
	}

	// Initial handshake
	if err := g.handler.Handshake(ctx, client, server, sessionState); err != nil {
		slog.Error("Handshake error", "client", remoteAddr, "error", err)
		backend.Release(server)
		return
	}

	session := &middleware.Session{
		Client:     client,
		RemoteAddr: remoteAddr,
		State:      sessionState,
		Backend:    backend,
		Server:     server,
		Buffer:     buf,
	}

	// Transaction loop
	for {
		// Poison-pill protection: Apply query timeout
		qctx, qcancel := context.WithTimeout(ctx, g.queryTimeout)

		// Read from client
		n, err := client.Read(buf)
		if err != nil {
			qcancel()
			if session.Server != nil {
				session.Backend.Release(session.Server)
			}
			if n > 0 || err != net.ErrClosed {
				slog.Debug("Client read error or close", "client", remoteAddr, "error", err)
			}
			return
		}

		session.Data = buf[:n]

		// Fast Path: Check if this is a query message ('Q' or 'P')
		isQuery := n > 0 && (session.Data[0] == 'Q' || session.Data[0] == 'P')

		if isQuery {
			// Track session state changes
			g.handler.TrackSessionState(session.State, session.Data)
			g.handler.TrackPreparedStatement(session.State, session.Data)
		}

		session.Normalized = g.handler.NormalizeQuery(session.Data)
		session.QueryInfo = g.handler.ClassifyQuery(session.Data)

		// Execute through middleware chain
		if err := g.chain.Handle(qctx, session, g.executeRequest); err != nil {
			slog.Error("Request failed", "client", remoteAddr, "error", err)
			qcancel()
			if session.Server != nil {
				session.Backend.Release(session.Server)
				session.Server = nil
			}
			return
		}

		// Transaction-level pooling: release connection if idle
		// but only if the session is not pinned (LISTEN, Advisory Locks, etc.)
		if session.State.TxState == protocol.StateIdle && session.Server != nil && !g.handler.IsPinned(session.State) {
			session.Backend.Release(session.Server)
			session.Server = nil
			session.Backend = nil
		}
		qcancel()
	}
}

func (g *Gateway) acquireBackend(ctx context.Context, hint balancer2.Hint) (pool2.Backend, net.Conn, error) {
	var lastErr error
	for i := range 3 {
		// If in failover, wait for resolution
		if !hint.ReadOnly {
			g.failoverMu.RLock()
			for g.inFailover.Load() {
				g.pauseCond.Wait()
			}
			g.failoverMu.RUnlock()
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

func (g *Gateway) triggerFailover() {
	if g.inFailover.CompareAndSwap(false, true) {
		slog.Warn("Primary lost, entering failover wait mode")
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
					backend, err := g.balancer.Next(context.Background(), balancer2.Hint{ReadOnly: false})
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
func (g *Gateway) CacheManager() *cache.Manager {
	return g.cacheManager
}

func (g *Gateway) proxyResponse(client, server net.Conn, buf []byte, capture *bytes.Buffer, readOnly bool) (protocol.TransactionState, bool, time.Duration, error) {
	// Fast Path: If it's a simple read-only query and no capture/firewall needed, use optimized steering
	if readOnly && capture == nil && (g.fwConfig == nil || g.fwConfig.MaxResponseSizeMB == 0) {
		return g.proxyResponseFastPath(client, server, buf)
	}

	return g.proxyResponseWithCapture(client, server, buf, capture)
}

func (g *Gateway) proxyResponseFastPath(client, server net.Conn, buf []byte) (protocol.TransactionState, bool, time.Duration, error) {
	start := time.Now()
	firstByte := true
	var rtt time.Duration

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

			// Still need to peek at transaction state to know when the response ends
			state, _ := g.handler.PeekTransactionState(buf[:n])
			if state != protocol.StatePartial {
				return state, false, rtt, nil
			}
		}
		if err != nil {
			return protocol.StateError, false, rtt, err
		}
	}
}

func (g *Gateway) proxyResponseWithCapture(client, server net.Conn, buf []byte, capture *bytes.Buffer) (protocol.TransactionState, bool, time.Duration, error) {
	isReadOnlyErr := false
	var rtt time.Duration
	start := time.Now()
	firstByte := true
	var totalSize int64
	maxSize := int64(0)
	if g.fwConfig != nil && g.fwConfig.MaxResponseSizeMB > 0 {
		maxSize = g.fwConfig.MaxResponseSizeMB * 1024 * 1024
	}

	for {
		n, err := server.Read(buf)
		if n > 0 {
			totalSize += int64(n)
			if maxSize > 0 && totalSize > maxSize {
				slog.Error("Response size exceeded maximum limit (Exfiltration Guard)", "size", totalSize, "limit", maxSize)
				observability2.DefaultTracker.RecordFirewallViolation("size")
				observability2.FirewallViolations.WithLabelValues("size").Inc()
				return protocol.StateError, false, rtt, fmt.Errorf("response size limit exceeded")
			}
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

			// Peek at transaction state
			state, _ := g.handler.PeekTransactionState(buf[:n])

			// If we reached a stable state (Idle, InTransaction, or Error), we are done with this response.
			if state != protocol.StatePartial {
				return state, isReadOnlyErr, rtt, nil
			}
		}
		if err != nil {
			return protocol.StateError, isReadOnlyErr, rtt, err
		}
	}
}

package proxy

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
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
	// maxCaptureBytes bounds a reply held for the cache or the collapser.
	maxCaptureBytes int
	// maxMessageBytes bounds a single client message assembled across reads.
	maxMessageBytes int

	// cancels maps a backend process id to the server it runs on, so an
	// out-of-band cancel request can be routed to it.
	cancels *cancelRegistry
	chain   middleware.Chain
	runtime atomic.Pointer[runtimeConfig]

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
	g.maxCaptureBytes = defaultMaxCaptureBytes
	g.maxMessageBytes = defaultMaxMessageBytes
	g.cancels = newCancelRegistry()
	g.config = cfg
	g.monitor = m
	g.backendTLS = backendTLS

	g.reconfigure(cfg)
	return g
}

// withQueryTimeout bounds one statement, unless the bound is disabled.
// readRequest reads one whole client message, however many TCP reads that takes.
//
// A read is not a message. This loop used to forward whatever a single 32 KiB
// read produced, so a statement larger than the buffer reached the backend as a
// fragment; the backend waited for the rest, sent nothing, and the session hung
// until query_timeout killed it. Every statement over 32 KiB failed that way.
//
// The common case — a whole message, or several, in one read — returns the read
// buffer untouched and allocates nothing. Only a message that genuinely spans
// reads is assembled, and only up to maxMessageBytes: the length driving that
// allocation is a number the client chose.
func (g *Gateway) readRequest(client net.Conn, buf []byte) ([]byte, error) {
	n, err := client.Read(buf)
	if err != nil || n == 0 {
		return buf[:n], err
	}

	framer, ok := g.handler.(protocol.MessageFramer)
	if !ok {
		// A protocol that cannot frame keeps the older behaviour rather than
		// being given a wrong answer about where its messages end.
		return buf[:n], nil
	}

	if trailingNeed(framer, buf[:n]) == 0 {
		return buf[:n], nil
	}
	return g.readWholeMessage(client, buf[:n], framer)
}

// trailingNeed reports how many more bytes are needed before every message in
// data is complete, or 0 if they already are.
//
// Every message, not just the first: a client may pipeline several, and a read
// can end part-way through any of them. Stopping at the first complete message
// would forward a fragment of the second and leave the backend waiting for the
// rest of it — the same hang, one message further along.
func trailingNeed(framer protocol.MessageFramer, data []byte) int {
	for len(data) > 0 {
		total, ok := framer.MessageLength(data)
		if !ok {
			// The header itself is incomplete; one more byte at least.
			return 1
		}
		if total > len(data) {
			return total - len(data)
		}
		data = data[total:]
	}
	return 0
}

// readWholeMessage keeps reading until nothing in the buffer is half-finished.
func (g *Gateway) readWholeMessage(client net.Conn, first []byte, framer protocol.MessageFramer) ([]byte, error) {
	data := slices.Clone(first)

	for {
		need := trailingNeed(framer, data)
		if need == 0 {
			return data, nil
		}

		// The size being reserved comes from a length the client supplied, so
		// it is bounded rather than trusted.
		if len(data)+need > g.maxMessageBytes {
			return data, fmt.Errorf("client message of %d bytes exceeds max_message_bytes of %d",
				len(data)+need, g.maxMessageBytes)
		}

		// need is 1 only when a header arrived split, where the real shortfall
		// is not yet knowable — read a little in that case rather than one byte
		// at a time.
		if need == 1 {
			need = postgresHeaderProbe
		}

		start := len(data)
		data = append(data, make([]byte, need)...)
		read, err := io.ReadFull(client, data[start:])
		data = data[:start+read]
		if err != nil {
			// A short read is only an error if it left a message unfinished,
			// which the next pass through the loop decides.
			if trailingNeed(framer, data) == 0 {
				return data, nil
			}
			return data, err
		}
	}
}

// postgresHeaderProbe is how much to read when a message header itself arrived
// split. Headers are five bytes, so one small read always completes one.
const postgresHeaderProbe = 8

// replaySession restores the client's session onto a freshly acquired
// connection, and refuses to proceed if it cannot.
//
// The connection is destroyed rather than returned: a replay that failed
// part-way leaves it holding some of one session's settings, which is worse
// for the next borrower than no connection at all.
func (g *Gateway) replaySession(ctx context.Context, s *middleware.Session) error {
	err := g.handler.ReplaySessionState(ctx, s.Server, s.State)
	if err == nil {
		err = g.handler.ReplayPreparedStatements(ctx, s.Server, s.State)
	}
	if err == nil {
		return nil
	}

	slog.Error("Could not restore the session on a new backend connection",
		"client", s.RemoteAddr, "error", err)

	if broken, ok := s.Server.(interface{ MarkBroken() }); ok {
		broken.MarkBroken()
	}

	_, _ = s.Client.Write(protocol.ErrorResponse(
		protocol.SeverityFatal, protocol.SQLStateAdminShutdown,
		"terminating connection: Pontus could not restore this session on a new backend connection"))

	return err
}

func (g *Gateway) withQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if g.queryTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, g.queryTimeout)
}

func (g *Gateway) reconfigure(cfg *config.Options) {
	// Nil-checked: Cache is a pointer and an unset `cache:` block leaves it nil.
	// The bound applies even with the cache disabled, because request
	// collapsing captures replies too.
	if cfg.MaxMessageBytes > 0 {
		g.maxMessageBytes = cfg.MaxMessageBytes
	}

	if cfg.Cache != nil && cfg.Cache.MaxEntrySize > 0 {
		g.maxCaptureBytes = cfg.Cache.MaxEntrySize
	}

	// A negative value disables the bound; see config.Options.QueryTimeout for
	// why that is spelled with a sign rather than a zero.
	if cfg.QueryTimeout != 0 {
		g.queryTimeout = cfg.QueryTimeout
	}
	// How stale a replica may be before reads stop going to it. Applied on
	// reload as well as at startup: an operator raising this during a
	// replication backlog should not have to restart the proxy.
	// What Pontus presents to clients that ask for encryption.
	//
	// PostgreSQL negotiates TLS inside the protocol rather than at the transport
	// layer, so this cannot be a tls.NewListener around the accept loop — the
	// handler answers the client's SSLRequest and upgrades in place. The `tls:`
	// block was parsed and reached nothing, so every client session ran in the
	// clear (finding A3).
	if clientTLS, err := CreateTLSConfig(cfg.TLS); err != nil {
		slog.Error("tls is configured but unusable; client connections stay in plaintext",
			"error", err)
	} else {
		protocol.SetClientTLS(clientTLS)
		if clientTLS != nil {
			slog.Info("Client TLS enabled")
		}
	}

	fo := cfg.FailoverOptions()
	balancer2.SetMaxReplicaLag(fo.MaxReplicaLag)
	// Whether a replica that lost its WAL receiver is pulled from the read pool,
	// and how long it must stream cleanly before it is trusted again.
	pool2.SetAutoReattach(*fo.AutoReattach, fo.AutoReattachInterval)
	// How long a session may queue for a connection when the pool is at its
	// ceiling. Applied on reload as well as at startup: an operator riding out
	// a burst should not have to restart the proxy to widen the queue.
	pool2.SetWaitTimeout(cfg.PoolWaitTimeout)
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

	// Rebuild the session on this connection.
	//
	// A failure here is fatal to the session, not a warning. Both calls were
	// logged and stepped over, so the statement then ran on a connection that
	// had not been configured — with the search_path of whoever used it last,
	// or, if the SET ROLE was the one that failed, with the privileges of the
	// pool identity rather than the session's. A session that cannot be
	// rebuilt has to end, not continue somewhere it was never set up.
	if s.ShouldReplay {
		if err := m.replaySession(ctx, s); err != nil {
			return err
		}
	}

	if _, err := s.Server.Write(s.Data); err != nil {
		_ = s.Backend.Release(s.Server)
		s.Server = nil
		return err
	}

	// Track only what the backend has actually been told, and only after the
	// been admitted. Recording the statement against this specific
	// connection is what stops a later replay re-parsing a name it already has.
	m.handler.TrackSessionState(s.State, s.Data)
	m.handler.TrackPreparedStatement(s.State, s.Data)

	// A statement the client closed must be forgotten, or the map grows for the
	// life of the session and replay re-parses statements the backend has
	// already been told to drop. A driver with a bounded statement cache closes
	// them continuously.
	if forgetter, ok := m.handler.(interface {
		ForgetPreparedStatement(*protocol.SessionState, []byte)
	}); ok {
		forgetter.ForgetPreparedStatement(s.State, s.Data)
	}
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
	//
	// Bounded, because a reply is streamed to the client but *held* here: an
	// unbounded capture buffers a whole result set in the proxy's heap, per
	// concurrent client, to serve a cache that would never store something
	// that size anyway.
	var capture *responseCapture
	switch {
	case s.ResponseCapture != nil:
		capture = newResponseCapture(s.ResponseCapture, m.maxCaptureBytes)
	case call != nil:
		capture = newResponseCapture(new(bytes.Buffer), m.maxCaptureBytes)
	}

	requestStart := time.Now()
	state, isReadOnlyErr, rtt, err := m.proxyResponse(ctx, s.Client, s.Server, s.Buffer, capture,
		s.QueryInfo.ReadOnly, m.handler.ResponseEndFor(s.Data), &s.ReplyFailed)
	if call != nil {
		call.data = capture.Bytes()
		call.err = cmp.Or(err, capture.Err())
	}
	if rtt > 0 {
		s.Backend.ReportRTT(rtt)
	}

	// Service time, as distinct from time-to-first-byte.
	//
	// Nothing reported this, so Backend.Latency() was always zero — and
	// CalculateCost returns early on a zero latency, which meant every backend
	// scored identically and least_conn, p2c and peak_ewma all ranked by a
	// constant. A load balancer that cannot tell an idle node from a saturated
	// one is not balancing.
	if elapsed := time.Since(requestStart); elapsed > 0 {
		s.Backend.ReportLatency(elapsed)
	}
	s.Backend.ReportResult(err)

	if isReadOnlyErr {
		// If we got a read-only error on primary, it might have been promoted/demoted
		s.Backend.ReevaluateRole()
	}

	// Update session state.
	//
	// A transaction ending is the only signal that transaction-scoped pins —
	// an explicit LOCK — have been released. Nothing on the request path marks
	// the end, so a session that took one lock stayed pinned for life.
	if state == protocol.StateIdle && s.State.TxState != protocol.StateIdle {
		if releaser, ok := m.handler.(interface {
			ReleaseTransactionPins(*protocol.SessionState)
		}); ok {
			releaser.ReleaseTransactionPins(s.State)
		}
	}
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
	// A timed-out read leaves the backend still working on the statement, so
	// the connection cannot go back to the pool: the next borrower would be
	// handed a socket that is about to emit the previous client's result set.
	//
	// This has to run *before* the release below, not after — marking a
	// connection broken once it is already back in the pool changes nothing.
	if errors.Is(err, os.ErrDeadlineExceeded) && s.Server != nil {
		if broken, ok := s.Server.(interface{ MarkBroken() }); ok {
			broken.MarkBroken()
		}
		slog.Warn("Query exceeded query_timeout; discarding the connection",
			"client", s.RemoteAddr, "timeout", m.queryTimeout)

		// Say so in the protocol. Returning the error closes the client
		// connection, and a bare close is indistinguishable from the database
		// dying — it points the operator at the network instead of at the
		// timeout that fired. FATAL because the session ends here: the backend
		// is still producing a result nobody will read.
		_, _ = s.Client.Write(protocol.ErrorResponse(
			protocol.SeverityFatal, protocol.SQLStateQueryCanceled,
			"canceling statement: exceeded Pontus query_timeout of "+m.queryTimeout.String()))
	}

	if s.Server != nil &&
		m.current().pooling.shouldReleaseAt(state, m.handler.IsPinned(s.State)) {
		if m.resetOnRelease() {
			releaseToPool(s.Backend, s.Server)
		} else {
			_ = s.Backend.Release(s.Server)
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

	backend, server, client, err := g.openSession(ctx, client, sessionState, remoteAddr)
	if errors.Is(err, errCancelHandled) {
		// The connection existed only to carry a cancel request. Nothing to
		// pool, nothing to reply to.
		return
	}
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

	// Remember where this session's backend process lives, so a cancel request
	// arriving later on a different connection can be routed to it. Dropped
	// when the session ends; the map is bounded by live sessions, not by
	// anything a client sends.
	if cancelPID := g.cancels.remember(sessionState.BackendKey, backendAddr(backend)); cancelPID != 0 {
		defer g.cancels.forget(cancelPID)
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
		// Wait for the client with no deadline of its own.
		//
		// The query timeout used to start here, before this read, so it was
		// counting while the client was simply idle — a session that sat quiet
		// for longer than query_timeout got an already-expired context for its
		// next statement and failed instantly. An idle connection is not a slow
		// query, and connection-pooling clients hold them idle by design.
		// A session that LISTENs is waiting for messages nobody asked for, and
		// they arrive precisely while it is idle here. Scoped to listeners: no
		// other session has anything to receive, so no other session pays for
		// a watcher.
		var watcher *asyncWatcher
		if session.Server != nil && sessionState.PinnedBy.Has(protocol.PinListen) {
			watcher = g.watchAsync(client, session.Server)
		}

		data, err := g.readRequest(client, buf)

		if watcher != nil {
			watcher.stop(session.Server)
		}

		if err != nil {
			releaseToPool(session.Backend, session.Server)
			if len(data) > 0 || err != net.ErrClosed {
				slog.Debug("Client read error or close", "client", remoteAddr, "error", err)
			}
			return
		}

		session.Data = data

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
			releaseToPool(session.Backend, session.Server)
			session.Server = nil
			return
		}

		// Session state is tracked in executeRequest, once the message has actually
		// been written to a backend.
		// onto the very connection about to parse it.

		session.Normalized = g.handler.NormalizeQuery(session.Data)
		session.QueryInfo = g.handler.ClassifyQuery(session.Data)

		// The clock starts now: there is a statement to run.
		qctx, qcancel := g.withQueryTimeout(ctx)

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
				_ = session.Backend.Release(session.Server)
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
				_ = session.Backend.Release(session.Server)
			}
			session.Server = nil
			session.Backend = nil
		}
		qcancel()
	}
}

// acquireBackend picks a backend and takes a connection from the pool belonging
// to this session's identity.
//
// The identity is part of acquisition rather than checked afterwards: a pool
// holding every user together keeps offering the wrong connection, and the
// check downstream then turns that into churn. Keying here is what makes the
// common case a hit.
func (g *Gateway) acquireBackendFor(ctx context.Context, hint balancer2.Hint, user, database string) (pool2.Backend, net.Conn, error) {
	return g.acquire(ctx, hint, user, database)
}

func (g *Gateway) acquireBackend(ctx context.Context, hint balancer2.Hint) (pool2.Backend, net.Conn, error) {
	return g.acquire(ctx, hint, "", "")
}

func (g *Gateway) acquire(ctx context.Context, hint balancer2.Hint, user, database string) (pool2.Backend, net.Conn, error) {
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
			conn, aerr := backend.AcquireFor(ctx, user, database)
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

func (g *Gateway) proxyResponse(ctx context.Context, client, server net.Conn, buf []byte, capture *responseCapture, readOnly bool, end protocol.ResponseEnd, failed *bool) (protocol.TransactionState, bool, time.Duration, error) {
	// query_timeout, made real.
	//
	// The deadline was carried on the context and the read loop never consulted
	// it, so a statement ran for as long as the database took — a twenty-second
	// pg_sleep completed happily under a three-second timeout. The whole point
	// of the setting is that one query cannot hold a pooled connection
	// indefinitely, and a context nothing reads does not bound anything.
	if deadline, ok := ctx.Deadline(); ok && g.queryTimeout > 0 {
		if err := server.SetReadDeadline(deadline); err != nil {
			return protocol.StateError, false, 0, err
		}
		defer func() { _ = server.SetReadDeadline(time.Time{}) }()
	}

	// Fast Path: a read-only query with nothing to capture can be steered straight through.
	if readOnly && capture == nil {
		return g.proxyResponseFastPath(client, server, buf, end, failed)
	}

	return g.proxyResponseWithCapture(client, server, buf, capture, end, failed)
}

func (g *Gateway) proxyResponseFastPath(client, server net.Conn, buf []byte, end protocol.ResponseEnd, failed *bool) (protocol.TransactionState, bool, time.Duration, error) {
	start := time.Now()
	firstByte := true
	var rtt time.Duration
	scan := protocol.NewReplyScanner(end)
	var pump *copyPump

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

			done, state := scan.Feed(buf[:n])

			// COPY turns the exchange around: the backend is now waiting for
			// the client, so the client's data has to start moving or neither
			// side speaks again.
			if scan.SawCopyIn() && pump == nil {
				pump = g.startCopyPump(client, server)
			}

			if done {
				if pump != nil {
					if perr := pump.stop(client); perr != nil {
						return protocol.StateError, false, rtt, perr
					}
				}
				setFailed(failed, scan.SawError())
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
// setFailed records whether the backend answered with an ErrorResponse.
func setFailed(failed *bool, sawError bool) {
	if failed != nil {
		*failed = sawError
	}
}

func midSequenceState(state protocol.TransactionState, end protocol.ResponseEnd) protocol.TransactionState {
	if !end.OnReadyForQuery {
		return protocol.StateInTransaction
	}
	return state
}

func (g *Gateway) proxyResponseWithCapture(client, server net.Conn, buf []byte, capture *responseCapture, end protocol.ResponseEnd, failed *bool) (protocol.TransactionState, bool, time.Duration, error) {
	isReadOnlyErr := false
	var rtt time.Duration
	start := time.Now()
	firstByte := true
	scan := protocol.NewReplyScanner(end)
	var pump *copyPump
	for {
		n, err := server.Read(buf)
		if n > 0 {
			if firstByte {
				rtt = time.Since(start)
				firstByte = false
			}
			capture.Write(buf[:n])
			if _, werr := client.Write(buf[:n]); werr != nil {
				return protocol.StateError, false, rtt, werr
			}

			if g.handler.IsReadOnlyError(buf[:n]) {
				isReadOnlyErr = true
			}

			// The reply ends at ReadyForQuery when the client sent a Sync, and
			// at the answer to its last message when it sent a Flush. Waiting
			// for ReadyForQuery either way blocks forever on the second.
			done, state := scan.Feed(buf[:n])

			if scan.SawCopyIn() && pump == nil {
				pump = g.startCopyPump(client, server)
			}

			if done {
				if pump != nil {
					if perr := pump.stop(client); perr != nil {
						return protocol.StateError, isReadOnlyErr, rtt, perr
					}
				}
				setFailed(failed, scan.SawError())
				return midSequenceState(state, end), isReadOnlyErr, rtt, nil
			}
		}
		if err != nil {
			return protocol.StateError, isReadOnlyErr, rtt, err
		}
	}
}

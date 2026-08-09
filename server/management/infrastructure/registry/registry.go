package registry

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/internal/system"
	"github.com/gsoultan/pontus/pkg/config"
	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/health"
	orchestration2 "github.com/gsoultan/pontus/server/internal/orchestration"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	protocol2 "github.com/gsoultan/pontus/server/internal/protocol"
	"github.com/gsoultan/pontus/server/management/infrastructure/state"
	"github.com/gsoultan/pontus/server/management/store"
	"github.com/gsoultan/pontus/server/proxy"
)

// Registry manages the state of all projects and proxies.
type Registry struct {
	mu          sync.RWMutex
	ctx         context.Context
	store       store.Project
	userStore   store.User
	projects    map[string]*state.Project
	dialTimeout time.Duration
	backendTLS  *tls.Config
	monitor     *system.Monitor
	// defaults carries the global settings that have no per-proxy field in
	// ProxyConfig — rate limit, cache, pooling mode. Without this
	// the gateway was built from five fields and every one of those features
	// was silently off, whatever config.yaml said.
	defaults *config.Options
}

func NewRegistry(ctx context.Context, store store.Project, userStore store.User, dialTimeout time.Duration, backendTLS *tls.Config, defaults *config.Options) *Registry {
	m := system.NewMonitor()
	m.Start(5 * time.Second)

	r := &Registry{
		ctx:         ctx,
		store:       store,
		userStore:   userStore,
		projects:    make(map[string]*state.Project),
		dialTimeout: dialTimeout,
		backendTLS:  backendTLS,
		monitor:     m,
		defaults:    defaults,
	}

	// Load and start projects
	for _, pcfg := range store.List() {
		stateObj, err := r.CreateProjectState(ctx, pcfg)
		if err != nil {
			slog.Error("Failed to start project", "id", pcfg.Id, "error", err)
			continue
		}
		r.projects[pcfg.Id] = stateObj
	}

	return r
}

func (r *Registry) GetProjectState(id string) (*state.Project, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stateObj, ok := r.projects[id]
	if !ok {
		return nil, fmt.Errorf("project not found: %s", id)
	}
	return stateObj, nil
}

func (r *Registry) CreateProjectState(ctx context.Context, pcfg *domain.Project) (*state.Project, error) {
	ctx, cancel := context.WithCancel(ctx)

	var handler protocol2.Handler
	switch strings.ToLower(pcfg.Protocol) {
	case "mysql":
		handler = protocol2.NewMySQLHandler()
	default:
		handler = protocol2.NewPostgresHandler()
	}

	stateObj := state.NewProject(pcfg, handler, r.backendTLS, ctx, cancel)

	for _, prcfg := range pcfg.Proxies {
		ps, err := r.CreateProxyState(ctx, prcfg, handler, pcfg.Protocol)
		if err != nil {
			slog.Error("Failed to create proxy", "id", prcfg.Id, "error", err)
			continue
		}
		stateObj.AddProxy(prcfg.Id, ps)
	}

	stateObj.SetInsightExplainer(&projectExplainer{registry: r, projectID: pcfg.Id})

	return stateObj, nil
}

func (r *Registry) CreateProxyState(ctx context.Context, prcfg *domain.ProxyConfig, handler protocol2.Handler, protocolName string) (*state.Proxy, error) {
	ctx, cancel := context.WithCancel(ctx)

	backends := make([]pool2.Backend, 0, len(prcfg.Backends))
	for _, bcfg := range prcfg.Backends {
		role := pool2.Role(bcfg.Role)
		if role == "" {
			role = pool2.RolePrimary
		}
		weight := int(bcfg.Weight)
		if weight <= 0 {
			weight = 1
		}

		agentAddr := bcfg.AgentAddress
		backendAddr := bcfg.Address
		if agentAddr == "" && backendAddr != "" {
			host, _, _ := net.SplitHostPort(backendAddr)
			agentAddr = net.JoinHostPort(host, "9091")
		} else if agentAddr != "" && backendAddr == "" {
			host, _, _ := net.SplitHostPort(agentAddr)
			backendAddr = net.JoinHostPort(host, "5432")
		}

		p, err := pool2.NewServer(backendAddr, bcfg.Zone, agentAddr, bcfg.AgentToken, role, weight, prcfg.MaxConns, 0, r.dialTimeout, handler, r.backendTLS, r.monitor, bcfg.AdminDsn)
		if err != nil {
			slog.Error("Failed to create backend server", "address", backendAddr, "error", err)
			continue
		}
		p.Start(ctx)
		backends = append(backends, p)
	}

	lb := newBalancer(prcfg.Balancer, backends)

	targets := make([]health.Target, len(backends))
	for i, b := range backends {
		targets[i] = b
	}
	healthInterval, healthTimeout := healthTiming(r.defaults)
	monitor := health.NewMonitor(targets, healthInterval, healthTimeout)
	go monitor.Start(ctx)

	var provisioner orchestration2.Provisioner
	if protocolName == "postgres" {
		provisioner = orchestration2.NewPostgresProvisioner(func() []pool2.Backend { return backends }, handler)
	}
	applyAgentTLS(r.defaults)
	failoverMgr := orchestration2.NewFailoverManager(provisioner, nil,
		func() []pool2.Backend { return backends }, failoverOptions(r.defaults))
	go failoverMgr.Start(ctx)

	proxyCfg := &config.Options{
		ProxyAddr:    prcfg.Address,
		Protocol:     protocolName,
		DialTimeout:  r.dialTimeout,
		MaxConns:     prcfg.MaxConns,
		QueryTimeout: 30 * time.Second,
		Balancer:     prcfg.Balancer,
	}
	// Inherit the global data-plane settings. ProxyConfig has no fields for
	// these, so without inheriting them the rate limiter and result cache are
	// never enabled for any proxy the registry builds.
	if d := r.defaults; d != nil {
		// The zone this proxy runs in. Without it the balancer's locality
		// penalty never applies, because CallerZone is empty on every hint.
		proxyCfg.LocalZone = d.LocalZone
		proxyCfg.RateLimit = d.RateLimit
		proxyCfg.Cache = d.Cache
		proxyCfg.ShadowBackends = d.ShadowBackends
		if d.PoolingMode != "" {
			proxyCfg.PoolingMode = d.PoolingMode
		}
		if d.QueryTimeout > 0 {
			proxyCfg.QueryTimeout = d.QueryTimeout
		}
	}

	gateway := proxy.NewGateway(handler, lb, failoverMgr, proxyCfg, r.backendTLS)

	// Nil keeps passthrough, which is the default. Set only when the operator
	// asked for it and a credential source actually resolved.
	gateway.SetCredentialStore(buildCredentialStore(r.defaults, backends))

	ln, err := net.Listen("tcp", prcfg.Address)
	if err != nil {
		cancel()
		for _, b := range backends {
			b.Close()
		}
		return nil, fmt.Errorf("failed to listen on %s: %w", prcfg.Address, err)
	}

	ps := state.NewProxy(prcfg, gateway, ln, backends, lb, monitor, failoverMgr, gateway.CacheManager(), ctx, cancel)

	// Hand the gateway the registry so a CDC consumer is accounted for. Without
	// it the data plane refuses replication rather than carrying an untracked
	// stream that the dashboard cannot see and the budget cannot bound.
	gateway.SetStreamRegistry(ps.Streams)

	go func() {
		if err := gateway.Serve(ctx, ln); err != nil && ctx.Err() == nil {
			slog.Error("Proxy server error", "proxy", prcfg.Id, "error", err)
		}
	}()

	return ps, nil
}

func (r *Registry) ListProjects() []*domain.Project {
	return r.store.List()
}

func (r *Registry) UpsertProject(pcfg *domain.Project) error {
	return r.store.Upsert(pcfg)
}

func (r *Registry) DeleteProject(id string) error {
	return r.store.Delete(id)
}

func (r *Registry) AddProject(id string, stateObj *state.Project) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projects[id] = stateObj
}

func (r *Registry) RemoveProject(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.projects, id)
}

func (r *Registry) UpdateConfig(cfg *config.Options) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.projects {
		for _, pr := range p.Proxies {
			pr.Gateway.UpdateConfig(cfg)
		}
	}
}

type projectExplainer struct {
	registry  *Registry
	projectID string
}

func (pe *projectExplainer) Explain(ctx context.Context, query string) (string, error) {
	return pe.registry.Explain(ctx, pe.projectID, query)
}

func (r *Registry) Explain(ctx context.Context, projectID string, query string) (string, error) {
	return "", fmt.Errorf("explain not implemented")
}

func (r *Registry) Monitor() *system.Monitor {
	return r.monitor
}

func (r *Registry) UserStore() store.User {
	return r.userStore
}

func (r *Registry) DialTimeout() time.Duration {
	return r.dialTimeout
}

// newBalancer resolves the configured strategy name to an implementation.
//
// Four of the six strategies used to be unreachable: the switch handled
// "consistent" and sent everything else to round-robin, so a deployment asking
// for p2c or least-conns silently got round-robin instead. An unknown name now
// says so rather than pretending it was honoured.
const (
	defaultHealthInterval = 10 * time.Second
	minHealthTimeout      = 500 * time.Millisecond
	maxHealthTimeout      = 5 * time.Second
)

func newBalancer(name string, backends []pool2.Backend) balancer2.Balancer {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "round-robin", "roundrobin", "round_robin":
		return balancer2.NewRoundRobin(backends)
	case "weighted", "weighted-round-robin", "weightedroundrobin", "weighted_round_robin":
		return balancer2.NewWeightedRoundRobin(backends)
	// least_conn is the spelling the proxy-creation form stored for a long
	// time. It never matched, so those proxies silently ran round-robin.
	case "least-conns", "least-connections", "leastconn", "least_conn", "least_conns":
		return balancer2.NewLeastConn(backends)
	case "p2c", "power-of-two":
		return balancer2.NewP2C(backends)
	case "peak-ewma", "ewma", "peak_ewma":
		return balancer2.NewPeakEWMA(backends)
	case "consistent", "consistent-hash", "ip-hash", "ip_hash":
		return balancer2.NewConsistentHash(backends)
	default:
		slog.Warn("Unknown balancer strategy, falling back to round-robin",
			"configured", name)
		return balancer2.NewRoundRobin(backends)
	}
}

// healthTiming resolves the liveness probe cadence from configuration.
//
// health_interval was parsed, defaulted and merged while the monitor was
// hardcoded to 10s, so an operator tightening it changed nothing. The timeout
// is derived rather than configured separately: it only has to be short enough
// that a probe cannot still be running when the next one starts, and a second
// knob whose only valid range is "less than the interval" is a trap.
// applyAgentTLS installs the sidecar's transport security.
//
// Set here rather than in internal/app because server/internal is not
// importable from there, and this is already where configuration is translated
// into data-plane settings. Kept apart from the database dialer's TLS: the
// agent and the database are different peers with different names and usually
// different CAs, and sharing one config is what made this look configured when
// it was not. Without it the mandatory agent token crosses the network in
// cleartext; orchestration warns once on first use.
func applyAgentTLS(cfg *config.Options) {
	if cfg == nil {
		return
	}
	agentTLS, err := proxy.CreateTLSConfig(cfg.AgentTLS)
	if err != nil {
		slog.Error("agent_tls is configured but unusable; agent connections stay in cleartext",
			"error", err)
		return
	}
	orchestration2.SetAgentTLS(agentTLS)
}

// failoverOptions translates the config file's failover block into the data
// plane's own options struct. Nothing under server/internal reads config
// directly; this is where the two meet.
func failoverOptions(cfg *config.Options) orchestration2.Options {
	if cfg == nil {
		cfg = &config.Options{}
	}
	f := cfg.FailoverOptions()

	return orchestration2.Options{
		Enabled:              f.Enabled,
		FailureThreshold:     f.FailureThreshold,
		FollowPrimary:        f.FollowPrimary,
		FollowPrimaryTimeout: f.FollowPrimaryTimeout,
		AutoReattach:         *f.AutoReattach,
		AutoReattachInterval: f.AutoReattachInterval,
	}
}

func healthTiming(cfg *config.Options) (interval, timeout time.Duration) {
	interval = defaultHealthInterval
	if cfg != nil && cfg.HealthInterval > 0 {
		interval = cfg.HealthInterval
	}

	timeout = interval / 5
	if timeout < minHealthTimeout {
		timeout = minHealthTimeout
	}
	if timeout > maxHealthTimeout {
		timeout = maxHealthTimeout
	}
	// A probe must never outlive its own interval.
	if timeout >= interval {
		timeout = interval / 2
	}
	return interval, timeout
}

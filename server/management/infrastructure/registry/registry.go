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
}

func NewRegistry(ctx context.Context, store store.Project, userStore store.User, dialTimeout time.Duration, backendTLS *tls.Config) *Registry {
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

		p, err := pool2.NewServer(backendAddr, bcfg.Zone, agentAddr, bcfg.AgentToken, role, weight, prcfg.MaxConns, 0, r.dialTimeout, handler, r.backendTLS, r.monitor)
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
	monitor := health.NewMonitor(targets, 10*time.Second, 2*time.Second)
	go monitor.Start(ctx)

	var provisioner orchestration2.Provisioner
	if protocolName == "postgres" {
		provisioner = orchestration2.NewPostgresProvisioner(func() []pool2.Backend { return backends }, handler)
	}
	failoverMgr := orchestration2.NewFailoverManager(provisioner, nil, func() []pool2.Backend { return backends })
	go failoverMgr.Start(ctx)

	proxyCfg := &config.Options{
		ProxyAddr:    prcfg.Address,
		Protocol:     protocolName,
		DialTimeout:  r.dialTimeout,
		MaxConns:     prcfg.MaxConns,
		QueryTimeout: 30 * time.Second,
	}

	gateway := proxy.NewGateway(handler, lb, failoverMgr, proxyCfg, r.backendTLS)

	ln, err := net.Listen("tcp", prcfg.Address)
	if err != nil {
		cancel()
		for _, b := range backends {
			b.Close()
		}
		return nil, fmt.Errorf("failed to listen on %s: %w", prcfg.Address, err)
	}

	ps := state.NewProxy(prcfg, gateway, ln, backends, lb, monitor, failoverMgr, gateway.CacheManager(), ctx, cancel)

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
func newBalancer(name string, backends []pool2.Backend) balancer2.Balancer {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "round-robin", "roundrobin":
		return balancer2.NewRoundRobin(backends)
	case "weighted", "weighted-round-robin", "weightedroundrobin":
		return balancer2.NewWeightedRoundRobin(backends)
	case "least-conns", "least-connections", "leastconn":
		return balancer2.NewLeastConn(backends)
	case "p2c", "power-of-two":
		return balancer2.NewP2C(backends)
	case "peak-ewma", "ewma":
		return balancer2.NewPeakEWMA(backends)
	case "consistent", "consistent-hash", "ip-hash":
		return balancer2.NewConsistentHash(backends)
	default:
		slog.Warn("Unknown balancer strategy, falling back to round-robin",
			"configured", name)
		return balancer2.NewRoundRobin(backends)
	}
}

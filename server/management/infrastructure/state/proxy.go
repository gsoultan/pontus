package state

import (
	"context"
	"net"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/cache"
	"github.com/gsoultan/pontus/server/internal/health"
	"github.com/gsoultan/pontus/server/internal/orchestration"
	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/replication"
	"github.com/gsoultan/pontus/server/proxy"
)

// Proxy holds the running state of a single proxy.
// defaultStreamBudget caps replication consumers per proxy. Deliberately small:
// each one holds a pool permit for hours, and exhausting this must turn away a
// new consumer rather than the application.
const defaultStreamBudget = 4

type Proxy struct {
	Config       *domain.ProxyConfig
	Gateway      *proxy.Gateway
	Listener     net.Listener
	Backends     []pool.Backend
	Balancer     balancer.Balancer
	Monitor      *health.Monitor
	FailoverMgr  *orchestration.FailoverManager
	CacheManager *cache.Manager
	// Streams tracks attached CDC consumers. They are pinned to one node and
	// hold a pool permit for their lifetime, so they are counted apart from
	// pooled sessions rather than folded into the connection total.
	Streams *replication.Registry
	Ctx     context.Context
	Cancel  context.CancelFunc
}

func NewProxy(config *domain.ProxyConfig, gateway *proxy.Gateway, listener net.Listener, backends []pool.Backend, bal balancer.Balancer, monitor *health.Monitor, failoverMgr *orchestration.FailoverManager, cacheMgr *cache.Manager, ctx context.Context, cancel context.CancelFunc) *Proxy {
	return &Proxy{
		Config:       config,
		Gateway:      gateway,
		Listener:     listener,
		Backends:     backends,
		Balancer:     bal,
		Monitor:      monitor,
		FailoverMgr:  failoverMgr,
		CacheManager: cacheMgr,
		Streams:      replication.NewRegistry(defaultStreamBudget),
		Ctx:          ctx,
		Cancel:       cancel,
	}
}

func (ps *Proxy) Stop() {
	if ps.Cancel != nil {
		ps.Cancel()
	}
	if ps.Listener != nil {
		ps.Listener.Close()
	}
	for _, b := range ps.Backends {
		b.Close()
	}
}

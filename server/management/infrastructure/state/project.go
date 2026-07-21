package state

import (
	"context"
	"crypto/tls"
	"sync"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/server/internal/insights"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// Project holds the running state of a project.
type Project struct {
	Mu            sync.RWMutex
	Config        *domain.Project
	Proxies       map[string]*Proxy
	InsightEngine *insights.Engine
	Handler       protocol.Handler
	BackendTLS    *tls.Config
	Ctx           context.Context
	Cancel        context.CancelFunc
}

func NewProject(config *domain.Project, handler protocol.Handler, backendTLS *tls.Config, ctx context.Context, cancel context.CancelFunc) *Project {
	return &Project{
		Config:     config,
		Proxies:    make(map[string]*Proxy),
		Handler:    handler,
		BackendTLS: backendTLS,
		Ctx:        ctx,
		Cancel:     cancel,
	}
}

func (p *Project) AddProxy(id string, ps *Proxy) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.Proxies[id] = ps
}

func (p *Project) SetInsightExplainer(explainer insights.QueryExplainer) {
	if p.InsightEngine != nil {
		p.InsightEngine.SetExplainer(explainer)
	}
}

func (p *Project) Stop() {
	if p.Cancel != nil {
		p.Cancel()
	}
	p.Mu.RLock()
	defer p.Mu.RUnlock()
	for _, ps := range p.Proxies {
		ps.Stop()
	}
}

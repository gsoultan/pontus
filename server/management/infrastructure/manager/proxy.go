package manager

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
)

// Proxy implements ProxyService.
type Proxy struct {
	registry *registry.Registry
}

func NewProxy(registry *registry.Registry) *Proxy {
	return &Proxy{registry: registry}
}

func (m *Proxy) AddProxy(ctx context.Context, req *endpoints.AddProxyRequest) (*endpoints.AddProxyResponse, error) {
	state, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	prcfg := req.Proxy
	if prcfg == nil {
		prcfg = &domain.ProxyConfig{}
	}
	if prcfg.Id == "" {
		prcfg.Id = uuid.New().String()
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	ps, err := m.registry.CreateProxyState(state.Ctx, prcfg, state.Handler, state.Config.Protocol)
	if err != nil {
		return nil, err
	}

	state.Proxies[prcfg.Id] = ps
	state.Config.Proxies = append(state.Config.Proxies, prcfg)

	if err := m.registry.UpsertProject(state.Config); err != nil {
		ps.Stop()
		delete(state.Proxies, prcfg.Id)
		return nil, err
	}

	return &endpoints.AddProxyResponse{Proxy: prcfg}, nil
}

func (m *Proxy) RemoveProxy(ctx context.Context, req *endpoints.RemoveProxyRequest) (*endpoints.RemoveProxyResponse, error) {
	state, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	ps, ok := state.Proxies[req.ProxyId]
	if !ok {
		return nil, fmt.Errorf("proxy not found")
	}

	ps.Stop()
	delete(state.Proxies, req.ProxyId)

	for i, p := range state.Config.Proxies {
		if p.Id == req.ProxyId {
			state.Config.Proxies = append(state.Config.Proxies[:i], state.Config.Proxies[i+1:]...)
			break
		}
	}

	if err := m.registry.UpsertProject(state.Config); err != nil {
		return nil, err
	}

	return &endpoints.RemoveProxyResponse{}, nil
}

func (m *Proxy) UpdateProxy(ctx context.Context, req *endpoints.UpdateProxyRequest) (*endpoints.UpdateProxyResponse, error) {
	state, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, err
	}

	state.Mu.Lock()
	defer state.Mu.Unlock()

	ps, ok := state.Proxies[req.Proxy.Id]
	if !ok {
		return nil, fmt.Errorf("proxy not found")
	}

	// Update logic - for simplicity, we replace and restart if address changed
	// In production, we'd do it more gracefully
	if ps.Config.Address != req.Proxy.Address {
		ps.Stop()
		newPs, err := m.registry.CreateProxyState(state.Ctx, req.Proxy, state.Handler, state.Config.Protocol)
		if err != nil {
			return nil, err
		}
		state.Proxies[req.Proxy.Id] = newPs
	} else {
		ps.Config = req.Proxy
	}

	for i, p := range state.Config.Proxies {
		if p.Id == req.Proxy.Id {
			state.Config.Proxies[i] = req.Proxy
			break
		}
	}

	if err := m.registry.UpsertProject(state.Config); err != nil {
		return nil, err
	}

	return &endpoints.UpdateProxyResponse{}, nil
}

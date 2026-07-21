package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/balancer"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

type GatewayManager struct {
	mu        sync.RWMutex
	gateways  map[string]*Gateway
	configs   map[string]*config.Options
	listeners map[string]net.Listener
}

func NewGatewayManager() *GatewayManager {
	return &GatewayManager{
		gateways:  make(map[string]*Gateway),
		configs:   make(map[string]*config.Options),
		listeners: make(map[string]net.Listener),
	}
}

func (m *GatewayManager) AddProject(ctx context.Context, name string, cfg *config.Options, handler protocol.Handler, lb balancer.Balancer, orch FailoverOrchestrator, backendTLS *tls.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.gateways[name]; ok {
		return fmt.Errorf("project %s already exists", name)
	}

	ln, err := net.Listen("tcp", cfg.ProxyAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", cfg.ProxyAddr, err)
	}

	gw := NewGateway(handler, lb, orch, cfg, backendTLS)
	m.gateways[name] = gw
	m.configs[name] = cfg
	m.listeners[name] = ln

	go func() {
		if err := gw.Serve(ctx, ln); err != nil {
			slog.Error("Gateway server failed", "project", name, "error", err)
		}
	}()

	slog.Info("Project gateway started", "project", name, "addr", cfg.ProxyAddr)
	return nil
}

func (m *GatewayManager) RemoveProject(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	gw, ok := m.gateways[name]
	if !ok {
		return fmt.Errorf("project %s not found", name)
	}

	ln := m.listeners[name]
	ln.Close()

	if err := gw.Stop(ctx); err != nil {
		slog.Error("Failed to stop gateway gracefully", "project", name, "error", err)
	}

	delete(m.gateways, name)
	delete(m.configs, name)
	delete(m.listeners, name)

	slog.Info("Project gateway stopped", "project", name)
	return nil
}

func (m *GatewayManager) GetProject(name string) (*Gateway, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	gw, ok := m.gateways[name]
	return gw, ok
}

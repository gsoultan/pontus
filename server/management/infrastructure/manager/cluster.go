package manager

import (
	"context"
	"strconv"
	"time"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/service"
)

// Cluster implements ClusterService.
type Cluster struct {
	registry     *registry.Registry
	settingStore service.SettingProvider
}

func NewCluster(registry *registry.Registry, settingStore service.SettingProvider) *Cluster {
	return &Cluster{
		registry:     registry,
		settingStore: settingStore,
	}
}

func (m *Cluster) SetClusterConfig(ctx context.Context, req *endpoints.SetClusterConfigRequest) (*endpoints.SetClusterConfigResponse, error) {
	// Persist settings
	for k, v := range req.Parameters {
		if err := m.settingStore.Set(ctx, k, v); err != nil {
			return nil, err
		}
	}

	// Apply partial updates to gateways
	cfg := &config.Options{}
	applied := false

	if v, ok := req.Parameters["query_timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.QueryTimeout = d
			applied = true
		}
	}

	if v, ok := req.Parameters["max_conns"]; ok {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.MaxConns = int32(i)
			applied = true
		}
	}

	if v, ok := req.Parameters["balancer"]; ok {
		cfg.Balancer = v
		applied = true
	}

	if v, ok := req.Parameters["pooling_mode"]; ok {
		cfg.PoolingMode = v
		applied = true
	}

	if applied {
		m.registry.UpdateConfig(cfg)
	}

	return &endpoints.SetClusterConfigResponse{Success: true}, nil
}

func (m *Cluster) GetClusterConfig(ctx context.Context, _ *endpoints.GetClusterConfigRequest) (*endpoints.GetClusterConfigResponse, error) {
	settings, err := m.settingStore.List(ctx)
	if err != nil {
		return nil, err
	}

	params := make(map[string]string, len(settings))
	for _, s := range settings {
		params[s.Key] = s.Value
	}

	return &endpoints.GetClusterConfigResponse{
		Parameters: params,
	}, nil
}

func (m *Cluster) DiscoverCluster(ctx context.Context, req *endpoints.DiscoverClusterRequest) (*endpoints.DiscoverClusterResponse, error) {
	// Implementation for DiscoverCluster
	return &endpoints.DiscoverClusterResponse{}, nil
}

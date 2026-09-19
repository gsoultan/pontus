package manager

import (
	"context"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	// Parsed before anything is persisted. The order used to be the other way
	// round: every parameter was written to SQLite and *then* parsed, so
	// query_timeout: "banana" was stored, silently not applied, and reported as
	// Success — leaving a value in the settings store that no restart can make
	// sense of and that the dashboard renders back as the current setting.
	cfg := &config.Options{}
	applied := false

	if v, ok := req.Parameters["query_timeout"]; ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"query_timeout %q is not a duration: %v", v, err)
		}
		cfg.QueryTimeout = d
		applied = true
	}

	if v, ok := req.Parameters["max_conns"]; ok {
		i, err := strconv.Atoi(v)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"max_conns %q is not a number: %v", v, err)
		}
		if i <= 0 {
			return nil, status.Errorf(codes.InvalidArgument,
				"max_conns must be positive, got %d", i)
		}
		cfg.MaxConns = int32(i)
		applied = true
	}

	if v, ok := req.Parameters["balancer"]; ok {
		cfg.Balancer = v
		applied = true
	}

	if v, ok := req.Parameters["pooling_mode"]; ok {
		cfg.PoolingMode = v
		applied = true
	}

	// Persist only once every parameter has been understood.
	for k, v := range req.Parameters {
		if err := m.settingStore.Set(ctx, k, v); err != nil {
			return nil, err
		}
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

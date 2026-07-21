package manager

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/service"
)

// Setting manages the runtime settings for the cluster.
type Setting struct {
	registry *registry.Registry
	store    service.SettingProvider
}

// NewSetting creates a new Setting manager.
func NewSetting(registry *registry.Registry, store service.SettingProvider) *Setting {
	return &Setting{
		registry: registry,
		store:    store,
	}
}

// UpdateConfig updates the cluster configuration and applies it to all gateways.
func (m *Setting) UpdateConfig(ctx context.Context, params map[string]string) error {
	// Persist settings
	for k, v := range params {
		if err := m.store.Set(ctx, k, v); err != nil {
			return fmt.Errorf("failed to persist setting %s: %w", k, err)
		}
	}

	// Apply settings to gateways
	// In a real production system, we might want to reload the base config and then override.
	// For now, we'll construct a delta config or update specific fields.

	// Example: Update QueryTimeout
	if v, ok := params["query_timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			// Update running gateways
			// We need a way to get the current config and update it.
			// For simplicity, we'll assume the registry knows how to apply these.
			// Actually, let's just create a Options object with the new values.
			cfg := &config.Options{
				QueryTimeout: d,
			}

			if v, ok := params["max_conns"]; ok {
				if i, err := strconv.Atoi(v); err == nil {
					cfg.MaxConns = int32(i)
				}
			}

			// This is a partial update. Gateway.UpdateConfig should handle it.
			m.registry.UpdateConfig(cfg)
		}
	}

	return nil
}

func (m *Setting) ListSettings(ctx context.Context) ([]service.Setting, error) {
	return m.store.List(ctx)
}

package app

import (
	"encoding/json"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/server/management/store"
)

// MigrateProjects handles conversion of old projects.json format to the new multi-proxy structure.
func MigrateProjects(projectStore store.Project) {
	projects := projectStore.List()
	migrated := 0

	// We need to reload the raw data because proto unmarshal might have dropped old fields
	data, err := os.ReadFile("projects.json")
	if err != nil {
		return
	}

	var rawMap map[string]map[string]any
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return
	}

	for _, p := range projects {
		raw, ok := rawMap[p.Id]
		if !ok {
			continue
		}

		// Check if it's the old format (has proxy_addr but no proxies)
		if len(p.Proxies) == 0 {
			proxyAddr, hasAddr := raw["proxy_addr"].(string)
			if hasAddr {
				log.Printf("Migrating project %s to new multi-proxy structure", p.Id)

				// Reconstruct proxy configuration from raw data
				p.Proxies = []*domain.ProxyConfig{
					new(domain.ProxyConfig{
						Id:       uuid.New().String(),
						Name:     "Default Proxy",
						Address:  proxyAddr,
						Balancer: p.Protocol, // Fallback if missing
						MaxConns: 100,
					}),
				}

				if balancer, ok := raw["balancer"].(string); ok {
					p.Proxies[0].Balancer = balancer
				}
				if maxConns, ok := raw["max_conns"].(float64); ok {
					p.Proxies[0].MaxConns = int32(maxConns)
				}

				// Migrate backends
				if backends, ok := raw["backends"].([]any); ok {
					for _, b := range backends {
						bMap, ok := b.(map[string]any)
						if !ok {
							continue
						}
						addr, _ := bMap["address"].(string)
						role, _ := bMap["role"].(string)
						weight, _ := bMap["weight"].(float64)

						p.Proxies[0].Backends = append(p.Proxies[0].Backends, new(domain.BackendConfig{
							Address: addr,
							Role:    role,
							Weight:  int32(weight),
						}))
					}
				}

				projectStore.Upsert(p)
				migrated++
			}
		}
	}

	if migrated > 0 {
		log.Printf("Successfully migrated %d projects to new structure", migrated)
	}
}

package app

import (
	"encoding/json"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/server/management/store"
)

// MigrateProjects converts the old single-proxy projects.json shape into the
// multi-proxy structure.
//
// path is the legacy file, resolved against the data directory. It used to be
// the literal "projects.json", read relative to whatever directory the operator
// happened to start Pontus from — so on a service-managed host, where the
// working directory is / rather than the data directory, this returned at the
// first ReadFile and the conversion never ran.
//
// It must also run *before* migrateFromJSON renames the file aside, which it
// did not: the rename happened first and this then read a path that no longer
// existed. The exact case the function exists for was the case it skipped.
func MigrateProjects(projectStore store.Project, path string) {
	projects := projectStore.List()
	migrated := 0

	// The raw data is reloaded because proto unmarshalling drops the old
	// top-level fields this conversion is built from.
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: cannot read %s for the multi-proxy migration: %v", path, err)
		}
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
						Id:      uuid.New().String(),
						Name:    "Default Proxy",
						Address: proxyAddr,
						// Left empty when the legacy file names no balancer:
						// newBalancer reads "" as round-robin, its documented
						// default. This used to be seeded with p.Protocol, so a
						// file with no balancer produced a proxy whose strategy
						// was "postgres" — unrecognised, silently round-robin,
						// and stored in SQLite as a value no UI can explain.
						Balancer: "",
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

				if err := projectStore.Upsert(p); err != nil {
					log.Printf("Warning: cannot save the migrated project %s: %v", p.Id, err)
					continue
				}
				migrated++
			}
		}
	}

	if migrated > 0 {
		log.Printf("Successfully migrated %d projects to new structure", migrated)
	}
}

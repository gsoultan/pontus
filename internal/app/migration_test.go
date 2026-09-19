package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/pkg/system"
	"github.com/gsoultan/pontus/server/management/store"
)

// legacyProjectsJSON is the shape this migration exists for: a project with a
// top-level proxy_addr and no proxies array, written before the multi-proxy
// restructure.
const legacyProjectsJSON = `{
  "p1": {
    "id": "p1",
    "name": "prod",
    "protocol": "postgres",
    "proxy_addr": ":5432",
    "balancer": "least_conns",
    "max_conns": 250,
    "backends": [
      {"address": "10.0.0.1:5432", "role": "primary", "weight": 1},
      {"address": "10.0.0.2:5432", "role": "replica", "weight": 2}
    ]
  }
}`

// A legacy projects.json must end up as a project that actually serves
// something. Both migrations have to run, in the right order, against the same
// file: migrateFromJSON imports the records, MigrateProjects builds the proxy
// out of the raw fields proto unmarshalling dropped.
func TestLegacyProjectsJSONBecomesAServingProject(t *testing.T) {
	dir := t.TempDir()
	writeLegacyJSON(t, dir)

	a := newMigrationTestApp(t, dir)
	a.migrateFromJSON()

	projects := a.projectStore.List()
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}

	p := projects[0]
	if len(p.Proxies) != 1 {
		t.Fatalf("the migrated project has %d proxies, want 1 — it serves nothing",
			len(p.Proxies))
	}

	proxy := p.Proxies[0]
	if proxy.Address != ":5432" {
		t.Errorf("proxy address is %q, want \":5432\"", proxy.Address)
	}
	if proxy.Balancer != "least_conns" {
		t.Errorf("proxy balancer is %q, want \"least_conns\"", proxy.Balancer)
	}
	if proxy.MaxConns != 250 {
		t.Errorf("proxy max_conns is %d, want 250", proxy.MaxConns)
	}
	if len(proxy.Backends) != 2 {
		t.Fatalf("the migrated proxy has %d backends, want 2", len(proxy.Backends))
	}
}

// The balancer field was seeded with p.Protocol "as a fallback if missing", so
// a legacy file with no balancer produced a proxy whose strategy is "postgres".
// NewBalancer does not recognise that and silently falls back to round-robin,
// so the deployment quietly changed routing strategy during an upgrade.
func TestAMissingBalancerDoesNotBecomeTheProtocol(t *testing.T) {
	dir := t.TempDir()
	raw := map[string]map[string]any{
		"p1": {
			"id": "p1", "name": "prod", "protocol": "postgres",
			"proxy_addr": ":5432",
			"backends":   []any{map[string]any{"address": "10.0.0.1:5432", "role": "primary"}},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "projects.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	a := newMigrationTestApp(t, dir)
	a.migrateFromJSON()

	projects := a.projectStore.List()
	if len(projects) != 1 || len(projects[0].Proxies) != 1 {
		t.Fatalf("migration did not produce one project with one proxy: %#v", projects)
	}

	if got := projects[0].Proxies[0].Balancer; got == "postgres" {
		t.Errorf("balancer is %q — the protocol was used as a balancer strategy", got)
	}
}

// Migrations run on every start. A second one must not duplicate the records
// the first imported, and must not undo them.
func TestMigrationIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeLegacyJSON(t, dir)

	a := newMigrationTestApp(t, dir)
	a.migrateFromJSON()

	first := a.projectStore.List()
	if len(first) != 1 {
		t.Fatalf("got %d projects after the first run, want 1", len(first))
	}
	firstProxies := len(first[0].Proxies)

	// Second start, same data directory, same store.
	a.migrateFromJSON()

	second := a.projectStore.List()
	if len(second) != 1 {
		t.Fatalf("got %d projects after the second run, want 1", len(second))
	}
	if got := len(second[0].Proxies); got != firstProxies {
		t.Errorf("the second run changed the proxy count from %d to %d",
			firstProxies, got)
	}
}

// The migration reads and renames files. Doing that relative to the process's
// working directory rather than the data directory means it runs or does not
// run depending on where the operator happened to type ./pontus.
func TestMigrationUsesTheDataDirectoryNotTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeLegacyJSON(t, dir)

	// A working directory that is deliberately not the data directory, and
	// deliberately has no projects.json in it.
	elsewhere := t.TempDir()
	restore, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(elsewhere); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(restore) })

	a := newMigrationTestApp(t, dir)
	a.migrateFromJSON()

	if got := len(a.projectStore.List()); got != 1 {
		t.Fatalf("got %d projects, want 1 — the migration looked in the working "+
			"directory instead of the data directory", got)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "projects.json.bak")); err == nil {
		t.Error("the migration wrote its backup into the working directory")
	}
}

func writeLegacyJSON(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "projects.json"),
		[]byte(legacyProjectsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newMigrationTestApp(t *testing.T, dataDir string) *App {
	t.Helper()

	path, err := system.GetDatabasePath("management.db", dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.NewManagementDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	a := NewApp(&config.Options{DataDir: dataDir})
	a.projectStore = store.NewSQLiteProject(db)
	a.userStore = store.NewSQLiteUser(db)
	return a
}

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// routingConfig appends a `databases:` table that aliases "app" to the
// app_prod database the harness creates.
const routingConfig = `
databases:
  - name: app
    database: app_prod
    max_conns: 2
  - name: "*"
    max_conns: 6
`

func connectNamed(t *testing.T, ctx context.Context, s *stack, database string) (*pgx.Conn, error) {
	t.Helper()
	return pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), s.proxyAddr, database))
}

// requireAliasTarget skips unless the backend has the database the alias points
// at, so a missing fixture reads as a skip rather than as a routing failure.
func requireAliasTarget(t *testing.T) {
	t.Helper()
	requireBackend(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/app_prod?sslmode=disable",
		backendUser(), backendPass(), backendAddr()))
	if err != nil {
		t.Skipf("no app_prod database on the backend: %v (createdb app_prod)", err)
	}
	conn.Close(context.Background())
}

// The alias has to reach the backend, not just the pool. `current_database()`
// is answered by PostgreSQL itself, so it reports where the connection actually
// landed rather than what Pontus recorded.
func TestDatabaseAliasRoutesToTheRealDatabase(t *testing.T) {
	requireAliasTarget(t)

	s := startStackWith(t, func(cfg string) string { return cfg + routingConfig })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := connectNamed(t, ctx, s, "app")
	if err != nil {
		t.Fatalf("connecting to the aliased database: %v", err)
	}
	defer conn.Close(context.Background())

	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatalf("asking the backend which database it opened: %v", err)
	}
	if database != "app_prod" {
		t.Errorf("current_database() = %q, want app_prod", database)
	}
}

// The same alias, with Pontus building the backend startup itself rather than
// forwarding the client's packet. Both paths have to agree, because they reach
// the backend by different code.
func TestDatabaseAliasRoutesUnderPontusAuth(t *testing.T) {
	requireAliasTarget(t)

	s := startStackWith(t, func(cfg string) string {
		return cfg + `
auth:
  mode: pontus
  cache_ttl: 30s
  negative_cache_ttl: 5s
  cache_size: 256
` + routingConfig
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := connectNamed(t, ctx, s, "app")
	if err != nil {
		t.Fatalf("connecting to the aliased database: %v", err)
	}
	defer conn.Close(context.Background())

	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatalf("asking the backend which database it opened: %v", err)
	}
	if database != "app_prod" {
		t.Errorf("current_database() = %q, want app_prod", database)
	}
}

// An unlisted database still reaches the database it named: the wildcard
// carries a limit and must not rewrite.
func TestUnlistedDatabaseIsNotRewritten(t *testing.T) {
	requireAliasTarget(t)

	s := startStackWith(t, func(cfg string) string { return cfg + routingConfig })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := connectNamed(t, ctx, s, backendDB())
	if err != nil {
		t.Fatalf("connecting to an unlisted database: %v", err)
	}
	defer conn.Close(context.Background())

	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatalf("asking the backend which database it opened: %v", err)
	}
	if database != backendDB() {
		t.Errorf("current_database() = %q, want %q — the wildcard rewrote it",
			database, backendDB())
	}
}

// The per-database ceiling is what makes one tenant's limit not everyone's.
// The admin console reports it, which is also the only way an operator can see
// that the rule took effect.
func TestPerDatabaseCeilingIsApplied(t *testing.T) {
	requireAliasTarget(t)

	s := startStackWith(t, func(cfg string) string {
		return cfg + `
auth:
  mode: pontus
  cache_ttl: 30s
  negative_cache_ttl: 5s
  cache_size: 256
admin_console:
  enabled: true
  users:
    - ` + backendUser() + `
` + routingConfig
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// One session on the bounded database, one on an unlisted one.
	bounded, err := connectNamed(t, ctx, s, "app")
	if err != nil {
		t.Fatalf("connecting to the bounded database: %v", err)
	}
	defer bounded.Close(context.Background())
	var n int
	if err := bounded.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("priming the bounded pool: %v", err)
	}

	free, err := connectNamed(t, ctx, s, backendDB())
	if err != nil {
		t.Fatalf("connecting to the unlisted database: %v", err)
	}
	defer free.Close(context.Background())
	if err := free.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("priming the unlisted pool: %v", err)
	}

	console, err := pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/pgbouncer?sslmode=disable",
		backendUser(), backendPass(), s.proxyAddr))
	if err != nil {
		t.Fatalf("connecting to the admin console: %v", err)
	}
	defer console.Close(context.Background())

	rows, err := console.Query(ctx, "SHOW POOLS")
	if err != nil {
		t.Fatalf("SHOW POOLS: %v", err)
	}
	defer rows.Close()

	ceilings := map[string]int64{}
	for rows.Next() {
		var database, user, poolMode, backend string
		var clActive, clWaiting, svActive, svIdle, svUsed, svTested, svLogin int64
		var maxwait, maxwaitUS int64
		if err := rows.Scan(&database, &user, &clActive, &clWaiting, &svActive,
			&svIdle, &svUsed, &svTested, &svLogin, &maxwait, &maxwaitUS,
			&poolMode, &backend); err != nil {
			t.Fatalf("scanning SHOW POOLS: %v", err)
		}
		ceilings[database] = svActive + svIdle
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading SHOW POOLS: %v", err)
	}

	// The aliased database appears under its *real* name, because that is the
	// database the pool actually holds connections to.
	if _, ok := ceilings["app_prod"]; !ok {
		t.Errorf("no pool reported for app_prod; got %v", ceilings)
	}
	if _, ok := ceilings["app"]; ok {
		t.Error("a pool was reported under the alias rather than the real database")
	}
}

// A routing table that cannot be served is refused at startup with the reason,
// rather than half-applied. Run directly rather than through the harness, which
// waits for a listener this process will never open.
func TestDuplicateDatabaseRuleRefusesToStart(t *testing.T) {
	requireBackend(t)

	root := repoRoot(t)
	dataDir := t.TempDir()
	binary := filepath.Join(dataDir, "pontus")

	build := exec.Command("go", "build", "-o", binary, "./cmd/pontus")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pontus: %v\n%s", err, out)
	}

	ports, releasePorts := freePorts(t, 2)
	config := configYAML(dataDir, fmt.Sprintf("127.0.0.1:%d", ports[0]),
		fmt.Sprintf("127.0.0.1:%d", ports[1])) + `
databases:
  - name: app
    database: one
  - name: app
    database: two
`
	releasePorts()

	configPath := filepath.Join(dataDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "-config", configPath)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PONTUS_AUTH_KEY="+authKey)
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("pontus started with a duplicate database rule\n%s", out)
	}
	if !strings.Contains(string(out), "listed twice") {
		t.Errorf("refusal does not name the cause:\n%s", out)
	}
}

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// consoleStack runs a proxy with the administration console enabled for the
// backend's own role.
func consoleStack(t *testing.T) *stack {
	t.Helper()
	requireBackend(t)

	return startStackWith(t, func(cfg string) string {
		return cfg + fmt.Sprintf(`
auth:
  mode: pontus
  cache_ttl: 30s
  negative_cache_ttl: 5s
  cache_size: 256
admin_console:
  enabled: true
  users:
    - %s
`, backendUser())
	})
}

func connectConsole(t *testing.T, ctx context.Context, s *stack, user string) (*pgx.Conn, error) {
	t.Helper()
	// "pgbouncer" is the database name every exporter and runbook already uses,
	// which is the whole reason the console answers on it.
	return pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/pgbouncer?sslmode=disable",
		user, backendPass(), s.proxyAddr))
}

// A real driver opens a session on the console and reads SHOW POOLS.
//
// This is the claim the feature makes: an exporter pointed at Pontus, with no
// change beyond the port, gets the rows it expects. A hand-rolled socket test
// proves the bytes; only a driver proves the startup sequence is one it accepts.
func TestAdminConsoleAnswersARealDriver(t *testing.T) {
	s := consoleStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Open an ordinary session first, so there is a pool to report on.
	app, err := connectAs(t, ctx, s, backendUser(), backendPass())
	if err != nil {
		t.Fatalf("opening an ordinary session: %v", err)
	}
	defer app.Close(context.Background())
	var one int
	if err := app.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("priming the pool: %v", err)
	}

	console, err := connectConsole(t, ctx, s, backendUser())
	if err != nil {
		t.Fatalf("connecting to the admin console: %v", err)
	}
	defer console.Close(context.Background())

	rows, err := console.Query(ctx, "SHOW POOLS")
	if err != nil {
		t.Fatalf("SHOW POOLS: %v", err)
	}
	defer rows.Close()

	// The column names are what an exporter reads. Getting them from the driver
	// rather than from our own encoder is what makes this a real check.
	var names []string
	for _, f := range rows.FieldDescriptions() {
		names = append(names, f.Name)
	}
	for _, want := range []string{"database", "user", "cl_active", "cl_waiting", "sv_idle"} {
		if !contains(names, want) {
			t.Errorf("SHOW POOLS has no %q column; got %v", want, names)
		}
	}

	var found int
	for rows.Next() {
		var database, user, poolMode, backend string
		var clActive, clWaiting, svActive, svIdle, svUsed, svTested, svLogin int64
		var maxwait, maxwaitUS int64
		if err := rows.Scan(&database, &user, &clActive, &clWaiting, &svActive,
			&svIdle, &svUsed, &svTested, &svLogin, &maxwait, &maxwaitUS,
			&poolMode, &backend); err != nil {
			t.Fatalf("scanning a SHOW POOLS row: %v", err)
		}
		// The numeric columns scanning into int64 is the point of declaring
		// their OID: an exporter that reads them as strings reports nothing.
		found++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading SHOW POOLS: %v", err)
	}
	if found == 0 {
		t.Error("SHOW POOLS returned no rows despite an open session")
	}
}

func TestAdminConsoleAnswersTheOtherCommands(t *testing.T) {
	s := consoleStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// An ordinary session, so SHOW CLIENTS has something to report. A console
	// session is not itself a pooled client: it never reaches a backend.
	app, err := connectAs(t, ctx, s, backendUser(), backendPass())
	if err != nil {
		t.Fatalf("opening an ordinary session: %v", err)
	}
	defer app.Close(context.Background())

	console, err := connectConsole(t, ctx, s, backendUser())
	if err != nil {
		t.Fatalf("connecting to the admin console: %v", err)
	}
	defer console.Close(context.Background())

	for _, command := range []string{
		"SHOW DATABASES", "SHOW CLIENTS", "SHOW LISTS", "SHOW CONFIG",
		"SHOW VERSION", "SHOW HELP",
	} {
		rows, err := console.Query(ctx, command)
		if err != nil {
			t.Errorf("%s: %v", command, err)
			continue
		}
		var n int
		for rows.Next() {
			n++
		}
		if err := rows.Err(); err != nil {
			t.Errorf("%s: %v", command, err)
		}
		rows.Close()
		if n == 0 {
			t.Errorf("%s returned no rows", command)
		}
	}
}

// A role that is not listed authenticates and is then told it may not use the
// console — not disconnected as if its password were wrong.
func TestAdminConsoleRefusesAnUnlistedRole(t *testing.T) {
	requireBackend(t)

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
    - somebody_else
`
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := connectConsole(t, ctx, s, backendUser())
	if err == nil {
		t.Fatal("an unlisted role reached the admin console")
	}
	// Specifically the authorisation refusal. "admin console" alone would also
	// match the passthrough-mode message, so this test would pass even if the
	// console had never been enabled.
	if !strings.Contains(err.Error(), "may not use the admin console") {
		t.Errorf("refusal does not name the cause: %v", err)
	}
}

// With no admin_console block the name is an ordinary database, which is what
// keeps the default deployment unchanged.
func TestConsoleDatabaseIsOrdinaryWhenDisabled(t *testing.T) {
	s := authStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// It should be routed to a backend, which does not have this database — so
	// the failure has to come from PostgreSQL, not from Pontus refusing it.
	_, err := connectConsole(t, ctx, s, backendUser())
	if err == nil {
		t.Fatal("a database no backend has was accepted")
	}
	if strings.Contains(err.Error(), "admin console") {
		t.Errorf("the console claimed the name while disabled: %v", err)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

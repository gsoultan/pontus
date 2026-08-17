//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// makeRole creates a login role on the primary for the duration of a test.
func makeRole(t *testing.T, name, password string) {
	t.Helper()

	admin, err := sql.Open("pgx", directDSN(backendAddr()))
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A GRANT leaves a dependency, so a bare DROP ROLE fails with 2BP01 on the
	// second run. Drop what the role owns and revoke what it was granted first.
	dropRole(ctx, t, admin, name)
	if _, err := admin.ExecContext(ctx,
		`CREATE ROLE `+name+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
	if _, err := admin.ExecContext(ctx,
		`GRANT CONNECT ON DATABASE `+backendDB()+` TO `+name); err != nil {
		t.Fatalf("grant connect to %s: %v", name, err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		a, err := sql.Open("pgx", directDSN(backendAddr()))
		if err != nil {
			return
		}
		defer a.Close()
		dropRole(c, t, a, name)
	})
}

// dropRole removes a role and everything that would keep it alive.
func dropRole(ctx context.Context, t *testing.T, db *sql.DB, name string) {
	t.Helper()

	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).Scan(&exists); err != nil || !exists {
		return
	}

	for _, stmt := range []string{
		`REVOKE ALL ON DATABASE ` + backendDB() + ` FROM ` + name,
		`DROP OWNED BY ` + name,
		`DROP ROLE IF EXISTS ` + name,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Logf("cleaning up %s: %v", name, err)
		}
	}
}

// Two users sharing one backend must both be served.
//
// A pooled connection carries the credentials it authenticated with, so one
// belonging to alice cannot serve bob. Pontus checks that — correctly — but the
// pool it checks against holds every identity together, so bob is repeatedly
// handed alice's connection. Whether that merely churns or actually fails is
// the question this answers, and it decides how urgent per-identity pooling is.
func TestTwoUsersShareABackend(t *testing.T) {
	requireBackend(t)

	makeRole(t, "pontus_alice", "alice-password")
	makeRole(t, "pontus_bob", "bob-password")

	s := authStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	type result struct{ ok, failed int }
	results := map[string]*result{"pontus_alice": {}, "pontus_bob": {}}

	// Interleaved, because that is what forces one user's connection to be the
	// idle one when the other asks.
	for round := range 10 {
		for _, user := range []string{"pontus_alice", "pontus_bob"} {
			password := "alice-password"
			if user == "pontus_bob" {
				password = "bob-password"
			}

			conn, err := connectAs(t, ctx, s, user, password)
			if err != nil {
				results[user].failed++
				t.Logf("round %d: %s could not connect: %v", round, user, err)
				continue
			}
			var who string
			err = conn.QueryRow(ctx,
				fmt.Sprintf("SELECT current_user /* r%d */", round)).Scan(&who)
			_ = conn.Close(context.Background())

			switch {
			case err != nil:
				results[user].failed++
				t.Logf("round %d: %s query failed: %v", round, user, err)
			case who != user:
				results[user].failed++
				t.Errorf("round %d: %s was served as %q — a connection authenticated "+
					"for one user answered for another", round, user, who)
			default:
				results[user].ok++
			}
		}
	}

	for user, r := range results {
		t.Logf("%s: ok=%d failed=%d", user, r.ok, r.failed)
		if r.failed > 0 {
			t.Errorf("%s failed %d of %d attempts sharing a backend with another user",
				user, r.failed, r.ok+r.failed)
		}
	}
}

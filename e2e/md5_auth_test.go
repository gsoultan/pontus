//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgreSQL has defaulted to SCRAM since 14, but a great many deployments
// still store md5 verifiers — an upgraded cluster keeps them until every
// password is reset. Refusing those strands the whole estate, so md5 has to
// work in both directions: verifying the client, and answering the backend.
//
// Unlike SCRAM there is no key to recover. The stored verifier is exactly what
// the client proves knowledge of, so the same value does both jobs.
func TestMD5RoleAuthenticatesThroughPontus(t *testing.T) {
	requireBackend(t)

	const role = "pontus_md5_user"
	const password = "md5-user-password"

	admin, err := sql.Open("pgx", directDSN(backendAddr()))
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The server's own encryption setting decides what a CREATE ROLE stores, so
	// it is switched for this role and put back afterwards.
	var previous string
	if err := admin.QueryRowContext(ctx, `SHOW password_encryption`).Scan(&previous); err != nil {
		t.Fatalf("read password_encryption: %v", err)
	}
	dropRole(ctx, t, admin, role)

	if _, err := admin.ExecContext(ctx, `SET password_encryption = 'md5'`); err != nil {
		t.Skipf("this server will not store md5 verifiers: %v", err)
	}
	if _, err := admin.ExecContext(ctx,
		`CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Skipf("could not create an md5 role: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		a, err := sql.Open("pgx", directDSN(backendAddr()))
		if err != nil {
			return
		}
		defer a.Close()
		dropRole(c, t, a, role)
		_, _ = a.ExecContext(c, `SET password_encryption = '`+previous+`'`)
	})

	// pg_hba decides what the *backend* will ask for, and it must agree with what
	// is stored. The cluster demands scram-sha-256 for every host connection, so
	// an md5 role cannot connect over TCP at all — not through Pontus and not
	// directly. Adding an md5 line for this one role is what makes the
	// configuration possible; first match wins, so it goes at the top.
	allowMD5ForRole(t, role)

	// Confirm the server really stored md5 rather than quietly upgrading it —
	// otherwise this test would pass while exercising SCRAM.
	var scheme string
	if err := admin.QueryRowContext(ctx,
		`SELECT left(coalesce(rolpassword,''), 3) FROM pg_authid WHERE rolname = $1`,
		role).Scan(&scheme); err != nil {
		t.Fatalf("read the stored verifier: %v", err)
	}
	if scheme != "md5" {
		t.Skipf("the server stored a %q verifier, not md5", scheme)
	}

	s := authStack(t)

	conn, err := connectAs(t, ctx, s, role, password)
	if err != nil {
		t.Fatalf("an md5 role could not authenticate through Pontus: %v\n=== proxy log ===\n%s",
			err, s.logs.String())
	}
	defer conn.Close(context.Background())

	var who string
	if err := conn.QueryRow(ctx, "SELECT current_user").Scan(&who); err != nil {
		t.Fatalf("query as an md5 role: %v", err)
	}
	if who != role {
		t.Errorf("served as %q, want %q", who, role)
	}

	// More than one statement, so the backend connection has to have been
	// opened by Pontus using the same stored verifier.
	for i := range 5 {
		var n int
		if err := conn.QueryRow(ctx, fmt.Sprintf("SELECT %d", i)).Scan(&n); err != nil {
			t.Fatalf("statement %d: %v", i, err)
		}
	}
}

// allowMD5ForRole prepends an md5 line to the primary's pg_hba for one role and
// removes it again afterwards.
//
// pg_hba decides what the backend asks for, and it has to agree with what is
// stored: a role with an md5 verifier cannot answer a scram-sha-256 challenge,
// so on a cluster that demands scram everywhere it cannot connect at all —
// through Pontus or directly. First match wins, so the line goes at the top.
func allowMD5ForRole(t *testing.T, role string) {
	t.Helper()

	const hba = "/var/lib/postgresql/data/pg_hba.conf"
	line := "host all " + role + " all md5"

	run := func(script string) (string, error) {
		return runtimeExec(t, primaryContainer(), "sh", "-c", script)
	}

	add := "grep -qF " + shellQuote(line) + " " + hba +
		" || sed -i " + shellQuote("1i "+line) + " " + hba
	if out, err := run(add); err != nil {
		t.Skipf("cannot edit pg_hba on this cluster: %v %s", err, out)
	}

	reload := "psql -U " + backendUser() + " -qtAc " + shellQuote("SELECT pg_reload_conf()")
	if out, err := run(reload); err != nil {
		t.Skipf("cannot reload pg_hba: %v %s", err, out)
	}

	t.Cleanup(func() {
		_, _ = run("grep -vxF " + shellQuote(line) + " " + hba + " > " + hba + ".tmp" +
			" && mv " + hba + ".tmp " + hba)
		_, _ = run(reload)
	})
}

// shellQuote wraps a value in single quotes for /bin/sh.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// A wrong password must be refused for md5 exactly as for SCRAM.
func TestMD5RoleWithAWrongPasswordIsRefused(t *testing.T) {
	requireBackend(t)
	s := authStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	if _, err := connectAs(t, ctx, s, "pontus_md5_user", "not-the-password"); err == nil {
		t.Fatal("a wrong password was accepted")
	}
}

//go:build e2e

package credentials

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func liveDB(t *testing.T) *sql.DB {
	t.Helper()

	addr := os.Getenv("PONTUS_E2E_BACKEND")
	if addr == "" {
		addr = "127.0.0.1:55832"
	}
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Skipf("no PostgreSQL at %s: %v", addr, err)
	}
	conn.Close()

	dsn := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		envOr("PONTUS_E2E_USER", "postgres"),
		envOr("PONTUS_E2E_PASSWORD", "postgres"),
		addr,
		envOr("PONTUS_E2E_DB", "postgres"))

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// The default query has to work against a real pg_authid, and the verifier it
// returns has to parse. Unit tests cover the parsing of synthetic values; only
// a real server proves the SQL is accepted and the format is what was assumed.
func TestAuthQueryAgainstRealPgAuthid(t *testing.T) {
	db := liveDB(t)

	store, err := NewQueryStore(SQLQuerier{DB: db}, "")
	if err != nil {
		t.Fatalf("NewQueryStore: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	user := envOr("PONTUS_E2E_USER", "postgres")
	verifier, err := store.Lookup(ctx, user)
	if err != nil {
		t.Fatalf("looking up %q against a real pg_authid: %v", user, err)
	}

	// Never log the verifier itself — only what kind it is.
	t.Logf("role %q stores a %s verifier", user, verifier.Method)

	switch verifier.Method {
	case MethodSCRAM:
		if verifier.SCRAM.Iterations <= 0 || len(verifier.SCRAM.StoredKey) != 32 {
			t.Errorf("SCRAM verifier parsed to iterations=%d storedKey=%d bytes",
				verifier.SCRAM.Iterations, len(verifier.SCRAM.StoredKey))
		}
	case MethodMD5:
		if len(verifier.MD5) != 32 {
			t.Errorf("md5 payload is %d characters, want 32", len(verifier.MD5))
		}
	default:
		t.Errorf("role %q has no usable password verifier (%s)", user, verifier.Method)
	}
}

func TestAuthQueryReportsUnknownUser(t *testing.T) {
	db := liveDB(t)
	store, _ := NewQueryStore(SQLQuerier{DB: db}, "")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := store.Lookup(ctx, "definitely-not-a-role-xyzzy"); err == nil {
		t.Fatal("an unknown role was accepted")
	}
}

// The documented deployment is a SECURITY DEFINER function so the auth user
// does not need superuser. If that recipe does not work, the documentation is
// wrong and every operator following it grants more than they had to.
func TestSecurityDefinerRecipeWorksWithoutSuperuser(t *testing.T) {
	db := liveDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	setup := []string{
		`CREATE OR REPLACE FUNCTION pontus_auth_lookup(IN wanted text,
		     OUT rolname text, OUT verifier text)
		 RETURNS record AS $$
		   SELECT rolname::text, coalesce(rolpassword, '')::text
		     FROM pg_authid WHERE rolname = wanted
		 $$ LANGUAGE sql SECURITY DEFINER STABLE`,
		`REVOKE EXECUTE ON FUNCTION pontus_auth_lookup(text) FROM PUBLIC`,
		`DROP ROLE IF EXISTS pontus_auth_probe`,
		`CREATE ROLE pontus_auth_probe LOGIN PASSWORD 'probe-password'`,
		`GRANT EXECUTE ON FUNCTION pontus_auth_lookup(text) TO pontus_auth_probe`,
	}
	for _, stmt := range setup {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setting up the SECURITY DEFINER recipe: %v\n%s", err, stmt)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = db.ExecContext(cleanupCtx, `DROP FUNCTION IF EXISTS pontus_auth_lookup(text)`)
		_, _ = db.ExecContext(cleanupCtx, `DROP ROLE IF EXISTS pontus_auth_probe`)
	})

	// Connect as the non-superuser auth role.
	addr := envOr("PONTUS_E2E_BACKEND", "127.0.0.1:55832")
	authDSN := fmt.Sprintf("postgres://pontus_auth_probe:probe-password@%s/%s?sslmode=disable",
		addr, envOr("PONTUS_E2E_DB", "postgres"))
	authDB, err := sql.Open("pgx", authDSN)
	if err != nil {
		t.Fatalf("open as the auth role: %v", err)
	}
	defer authDB.Close()

	// It must NOT be able to read pg_authid directly...
	var direct string
	err = authDB.QueryRowContext(ctx,
		`SELECT coalesce(rolpassword,'') FROM pg_authid WHERE rolname = $1`, "postgres").Scan(&direct)
	if err == nil {
		t.Error("the auth role could read pg_authid directly; it does not need that privilege")
	}

	// ...but it must be able to go through the function.
	store, err := NewQueryStore(SQLQuerier{DB: authDB},
		`SELECT rolname, verifier FROM pontus_auth_lookup($1)`)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := store.Lookup(ctx, envOr("PONTUS_E2E_USER", "postgres"))
	if err != nil {
		t.Fatalf("the documented SECURITY DEFINER recipe does not work: %v", err)
	}
	if verifier.Method == MethodNone {
		t.Error("the function returned no verifier")
	}
	t.Logf("non-superuser lookup via SECURITY DEFINER returned a %s verifier", verifier.Method)
}

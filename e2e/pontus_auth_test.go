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

// authStack runs a proxy that authenticates clients itself.
func authStack(t *testing.T) *stack {
	t.Helper()
	requireBackend(t)

	return startStackWith(t, func(cfg string) string {
		return cfg + `
auth:
  mode: pontus
  cache_ttl: 30s
  negative_cache_ttl: 5s
  cache_size: 256
`
	})
}

func connectAs(t *testing.T, ctx context.Context, s *stack, user, password string) (*pgx.Conn, error) {
	t.Helper()
	return pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		user, password, s.proxyAddr, backendDB()))
}

// The whole point: a real driver authenticates against Pontus, Pontus opens its
// own backend connection with the recovered ClientKey, and the session works.
//
// Nothing here forwards the client's startup packet. Pontus ran the SCRAM
// exchange itself, kept the ClientKey, and used it to open a connection as that
// user — which is the capability a session needs before it can ever be moved
// between backends.
func TestPontusAuthenticatesTheClientItself(t *testing.T) {
	s := authStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := connectAs(t, ctx, s, backendUser(), backendPass())
	if err != nil {
		t.Fatalf("connecting through a Pontus-authenticated proxy: %v", err)
	}
	defer conn.Close(context.Background())

	var n int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("query after Pontus-side authentication: %v", err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 returned %d", n)
	}

	// The session must survive more than the first statement.
	for i := range 10 {
		if err := conn.QueryRow(ctx, fmt.Sprintf("SELECT %d", i)).Scan(&n); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
}

// A wrong password must be refused by Pontus, and refused the same way an
// unknown user is — otherwise the error message enumerates real accounts.
func TestPontusAuthRejectsAWrongPassword(t *testing.T) {
	s := authStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	_, err := connectAs(t, ctx, s, backendUser(), "definitely-not-the-password")
	if err == nil {
		t.Fatal("a wrong password was accepted")
	}
	wrongPassword := err.Error()

	_, err = connectAs(t, ctx, s, "no-such-role-xyzzy", "anything")
	if err == nil {
		t.Fatal("an unknown role was accepted")
	}
	unknownUser := err.Error()

	if !strings.Contains(wrongPassword, "authentication failed") {
		t.Errorf("wrong password reported as %q", wrongPassword)
	}
	if !strings.Contains(unknownUser, "authentication failed") {
		t.Errorf("unknown user reported as %q; it must not be distinguishable "+
			"from a wrong password, or the error enumerates real accounts", unknownUser)
	}
}

// Passthrough stays the default, so a config with no auth block behaves exactly
// as before.
func TestPassthroughRemainsTheDefault(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	var n int
	if err := conn.QueryRow(ctx, "SELECT 7").Scan(&n); err != nil || n != 7 {
		t.Fatalf("the default passthrough path broke: %v (n=%d)", err, n)
	}
}

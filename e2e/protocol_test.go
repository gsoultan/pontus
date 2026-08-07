//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// connectMode opens a session pinned to a specific query execution mode.
func connectMode(t *testing.T, ctx context.Context, s *stack, mode pgx.QueryExecMode) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), s.proxyAddr, backendDB()))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.DefaultQueryExecMode = mode

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect (%v): %v", mode, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// Does the proxy observe queries sent over the extended protocol?
//
// Every mainstream driver — pgx, JDBC, psycopg3, node-postgres with prepared
// statements — uses Parse/Bind/Execute rather than the simple Query message.
// If Pontus only inspects simple queries then metrics undercount real traffic:
// every observability number the dashboard shows would be a fraction of what
// actually ran.
func TestProtocolCoverage(t *testing.T) {
	modes := []struct {
		name string
		mode pgx.QueryExecMode
	}{
		{"simple", pgx.QueryExecModeSimpleProtocol},
		{"extended", pgx.QueryExecModeExec},
	}

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			s := startStack(t)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			conn := connectMode(t, ctx, s, m.mode)

			const queries = 10
			for i := range queries {
				var n int
				if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
					t.Fatalf("query %d: %v", i, err)
				}
			}

			token := s.login()
			projectID, proxyID := s.project(token)

			var observed float64
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				out, code := s.rpc("GetStatus",
					map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
				if code == http.StatusOK {
					if observed = asFloat(out["totalRequests"]); observed > 0 {
						break
					}
				}
				time.Sleep(300 * time.Millisecond)
			}

			t.Logf("%s protocol: totalRequests = %v after %d queries", m.name, observed, queries)
			if observed < queries {
				t.Errorf("%s protocol: only %v of %d queries were observed by the proxy",
					m.name, observed, queries)
			}
		})
	}
}

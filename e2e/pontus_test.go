//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// connect opens a session through the proxy, not to the backend directly.
func connect(t *testing.T, ctx context.Context, s *stack) *pgx.Conn {
	t.Helper()
	dsn := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), s.proxyAddr, backendDB())

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect through proxy at %s: %v", s.proxyAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// The whole point of the product: a client speaks Postgres to Pontus and gets
// real rows back from the backend.
func TestQueriesFlowThroughTheProxy(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Errorf("SELECT 1 returned %d", one)
	}

	var version string
	if err := conn.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		t.Fatalf("SELECT version(): %v", err)
	}
	if !strings.Contains(strings.ToLower(version), "postgresql") {
		t.Errorf("unexpected version banner: %q", version)
	}
}

// Reads and writes both have to survive the pooler, including inside a
// transaction, where the session cannot be handed to another client mid-flight.
func TestReadWriteAndTransaction(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)
	table := fmt.Sprintf("pontus_e2e_%d", time.Now().UnixNano())

	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id int primary key, note text)", table)); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		direct, err := pgx.Connect(cleanupCtx, fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
			backendUser(), backendPass(), backendAddr(), backendDB()))
		if err != nil {
			return
		}
		defer direct.Close(cleanupCtx)
		_, _ = direct.Exec(cleanupCtx, "DROP TABLE IF EXISTS "+table)
	})

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for i := range 5 {
		if _, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (id, note) VALUES ($1, $2)", table), i, "row"); err != nil {
			t.Fatalf("INSERT %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}

	// A rolled-back transaction must leave nothing behind.
	tx2, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin 2: %v", err)
	}
	if _, err := tx2.Exec(ctx, fmt.Sprintf("INSERT INTO %s (id, note) VALUES (99, 'gone')", table)); err != nil {
		t.Fatalf("INSERT in rolled-back tx: %v", err)
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if count != 5 {
		t.Errorf("count after rollback = %d, want 5", count)
	}
}

// Traffic through the data plane has to show up in the control plane. This is
// the path that silently reported lifetime averages and blanked on restart.
func TestMetricsReflectTraffic(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)
	const queries = 25
	for i := range queries {
		var n int
		if err := conn.QueryRow(ctx, "SELECT $1::int", i).Scan(&n); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}

	token := s.login()
	projectID, proxyID := s.project(token)

	// Give the tracker a moment to observe the traffic.
	var status map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, code := s.rpc("GetStatus", map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
		if code == http.StatusOK {
			status = out
			if asFloat(out["totalRequests"]) > 0 {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if status == nil {
		t.Fatal("GetStatus never returned OK")
	}
	if got := asFloat(status["totalRequests"]); got < queries {
		t.Errorf("totalRequests = %v, want at least %d", got, queries)
	}
	if _, ok := status["backends"]; !ok {
		t.Error("GetStatus response has no backends field")
	}

	body := s.metrics()
	if !strings.Contains(body, "pontus") && !strings.Contains(body, "go_goroutines") {
		t.Errorf("/metrics does not look like a Prometheus exposition:\n%.400s", body)
	}
}

// StreamStatus replaced polling; it must actually deliver.
func TestStreamStatusDelivers(t *testing.T) {
	s := startStack(t)
	token := s.login()

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://%s/api.proto.service.ManagementService/StreamStatus", s.mgmtAddr),
		strings.NewReader(`{"intervalMs":500}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+token)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("StreamStatus: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StreamStatus returned %d", resp.StatusCode)
	}

	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil && n == 0 {
		t.Fatalf("StreamStatus produced no frame: %v", err)
	}
	if n == 0 {
		t.Error("StreamStatus produced an empty first frame")
	}
}

// The management API must not be reachable without credentials.
func TestManagementRequiresAuth(t *testing.T) {
	s := startStack(t)

	for _, method := range []string{"GetStatus", "ListProjects", "RemoveBackend"} {
		_, code := s.rpc(method, map[string]string{}, "")
		if code == http.StatusOK {
			t.Errorf("%s succeeded without a token", method)
		}
	}

	// A garbage token must not work either.
	if _, code := s.rpc("GetStatus", map[string]string{}, "not-a-real-token"); code == http.StatusOK {
		t.Error("GetStatus accepted a forged token")
	}

	// And a real one must.
	token := s.login()
	projectID, proxyID := s.project(token)
	if out, code := s.rpc("GetStatus", map[string]string{"projectId": projectID, "proxyId": proxyID}, token); code != http.StatusOK {
		t.Errorf("GetStatus rejected a valid token (%d): %v", code, out)
	}
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		var f float64
		_, _ = fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}

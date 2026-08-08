//go:build e2e

// Package e2e drives a real Pontus process against a real PostgreSQL backend.
//
// It is behind the `e2e` build tag because it needs a database; `go test ./...`
// must stay runnable on a machine that has none.
//
//	container run -d --name pontus-e2e-pg -p 5433:5432 \
//	  -e POSTGRES_PASSWORD=postgres postgres:17-alpine \
//	  -c wal_level=logical
//
// wal_level=logical is needed only by the slot tests, which skip without it.
//
//	go test -tags e2e ./e2e/...
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	authKey = "e2e-test-key-not-a-real-secret"

	// Generous: the first start compiles nothing but does open SQLite stores,
	// run migrations and bind two listeners.
	startupTimeout = 30 * time.Second
)

// backendAddr is the PostgreSQL the proxy sits in front of.
func backendAddr() string {
	if v := os.Getenv("PONTUS_E2E_BACKEND"); v != "" {
		return v
	}
	return "127.0.0.1:5433"
}

func backendUser() string { return envOr("PONTUS_E2E_USER", "postgres") }
func backendPass() string { return envOr("PONTUS_E2E_PASSWORD", "postgres") }
func backendDB() string   { return envOr("PONTUS_E2E_DB", "postgres") }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// stack is a running Pontus process plus the paths and ports it owns.
//
// Ports are allocated per stack rather than fixed: the suite starts a fresh
// process per test, and reusing one port pair made a test fail only when run
// alongside its neighbours, as teardown raced the next bind.
type stack struct {
	cmd       *exec.Cmd
	dataDir   string
	logs      *strings.Builder
	t         *testing.T
	proxyAddr string
	mgmtAddr  string
}

// freePort asks the kernel for an unused port and immediately releases it.
// A short race remains between release and bind, which is why startup also
// waits for the listener rather than assuming it is up.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// requireBackend skips the whole suite when there is no database to talk to,
// rather than failing and looking like a regression.
func requireBackend(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", backendAddr(), 2*time.Second)
	if err != nil {
		t.Skipf("no PostgreSQL at %s: %v (set PONTUS_E2E_BACKEND)", backendAddr(), err)
	}
	conn.Close()
}

// startStack builds the binary and runs it against a scratch data directory.
func startStack(t *testing.T) *stack {
	t.Helper()
	requireBackend(t)

	root := repoRoot(t)
	dataDir := t.TempDir()
	binary := filepath.Join(dataDir, "pontus")

	proxyAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	mgmtAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	build := exec.Command("go", "build", "-o", binary, "./cmd/pontus")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pontus: %v\n%s", err, out)
	}

	configPath := filepath.Join(dataDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configYAML(dataDir, proxyAddr, mgmtAddr)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	logs := &strings.Builder{}
	cmd := exec.Command(binary, "-config", configPath)
	cmd.Dir = root
	cmd.Stdout = logs
	cmd.Stderr = logs
	// A fresh data dir means the bootstrap admin password is generated and
	// printed; the test reads it back out of the log.
	cmd.Env = append(os.Environ(), "PONTUS_AUTH_KEY="+authKey)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start pontus: %v", err)
	}

	s := &stack{
		cmd: cmd, dataDir: dataDir, logs: logs, t: t,
		proxyAddr: proxyAddr, mgmtAddr: mgmtAddr,
	}
	t.Cleanup(s.stop)

	s.waitListening(proxyAddr)
	s.waitListening(mgmtAddr)
	return s
}

func (s *stack) stop() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _, _ = s.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
	}
}

func (s *stack) waitListening(addr string) {
	s.t.Helper()
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			conn.Close()
			return
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			s.t.Fatalf("pontus exited before listening on %s\n%s", addr, s.logs.String())
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.t.Fatalf("timed out waiting for %s\n%s", addr, s.logs.String())
}

// bootstrapPassword scrapes the one-time admin password out of the startup log.
func (s *stack) bootstrapPassword() string {
	s.t.Helper()
	// The bootstrap banner is logged as one slog record, so its newlines arrive
	// escaped. Cut the token at the first whitespace or backslash either way.
	text := s.logs.String()
	idx := strings.Index(text, "password: ")
	if idx >= 0 {
		rest := text[idx+len("password: "):]
		end := strings.IndexAny(rest, " \t\r\n\\")
		if end < 0 {
			end = len(rest)
		}
		if pw := strings.TrimSpace(rest[:end]); pw != "" {
			return pw
		}
	}
	s.t.Fatalf("bootstrap password not found in startup log:\n%s", s.logs.String())
	return ""
}

// rpc calls a management RPC over Connect's JSON protocol.
func (s *stack) rpc(method string, body any, token string) (map[string]any, int) {
	s.t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		s.t.Fatalf("marshal %s: %v", method, err)
	}

	url := fmt.Sprintf("http://%s/api.proto.service.ManagementService/%s", s.mgmtAddr, method)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		s.t.Fatalf("new request %s: %v", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		s.t.Fatalf("call %s: %v", method, err)
	}
	defer resp.Body.Close()

	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

func (s *stack) login() string {
	s.t.Helper()
	out, code := s.rpc("Login", map[string]string{
		"username": "admin",
		"password": s.bootstrapPassword(),
	}, "")
	if code != http.StatusOK {
		s.t.Fatalf("login failed (%d): %v\n%s", code, out, s.logs.String())
	}
	token, _ := out["token"].(string)
	if token == "" {
		s.t.Fatalf("login returned no token: %v", out)
	}
	return token
}

// project returns the id and proxy id of the project migrated from config.
// GetStatus is scoped to a proxy, so most calls need these.
func (s *stack) project(token string) (projectID, proxyID string) {
	s.t.Helper()

	out, code := s.rpc("ListProjects", map[string]string{}, token)
	if code != http.StatusOK {
		s.t.Fatalf("ListProjects failed (%d): %v", code, out)
	}

	projects, _ := out["projects"].([]any)
	if len(projects) == 0 {
		s.t.Fatalf("no projects were migrated from config: %v", out)
	}

	first, _ := projects[0].(map[string]any)
	projectID, _ = first["id"].(string)

	proxies, _ := first["proxies"].([]any)
	if len(proxies) > 0 {
		if p, ok := proxies[0].(map[string]any); ok {
			proxyID, _ = p["id"].(string)
		}
	}
	return projectID, proxyID
}

func (s *stack) metrics() string {
	s.t.Helper()
	resp, err := http.Get("http://" + s.mgmtAddr + "/metrics")
	if err != nil {
		s.t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("read /metrics: %v", err)
	}
	return string(body)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd) // e2e/ lives one level below the module root
}

// adminDSN is the administrative connection string handed to the proxy.
func adminDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), backendAddr(), backendDB())
}

func configYAML(dataDir, proxyAddr, mgmtAddr string) string {
	return fmt.Sprintf(`proxy_addr: "%s"
mgmt_addr: "%s"
protocol: postgres
pooling_mode: transaction
balancer: p2c
local_zone: e2e
data_dir: "%s"

dial_timeout: 5s
health_interval: 2s
query_timeout: 30s
max_conns: 20
min_idle: 1

backends:
  - addr: "%s"
    agent_addr: "127.0.0.1:19093"
    role: primary
    weight: 1
    zone: e2e
    # Pontus's own session. Client sessions carry the client's credentials, so
    # health probes, role detection and slot management need one of these.
    admin_dsn: "%s"

cache:
  enabled: true
  ttl: 5s
  max_size: 128

rate_limit:
  enabled: true
  rps: 500
  burst: 1000
`, proxyAddr, mgmtAddr, dataDir, backendAddr(), adminDSN())
}

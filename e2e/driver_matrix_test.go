//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Pontus-side authentication is a wire-protocol implementation, and a wire
// implementation is judged by clients that were written without reference to
// it. pgx passing proves the exchange is self-consistent; libpq and asyncpg
// prove it is *correct*, because neither has ever seen this code.
//
// The proxy binds 0.0.0.0 here so a client inside a container can reach it.
func startAuthStackOnAllInterfaces(t *testing.T) *stack {
	t.Helper()
	requireBackend(t)

	return startStackWith(t, func(cfg string) string {
		// Widen the bind address but keep the harness's port, so its own
		// readiness check on 127.0.0.1 still works.
		cfg = strings.Replace(cfg, `proxy_addr: "127.0.0.1:`, `proxy_addr: "0.0.0.0:`, 1)
		return cfg + `
auth:
  mode: pontus
  cache_ttl: 30s
`
	})
}

// proxyPort returns the port the harness gave this stack.
func proxyPort(t *testing.T, s *stack) string {
	t.Helper()
	_, port, ok := strings.Cut(s.proxyAddr, ":")
	if !ok {
		t.Fatalf("cannot read a port from %q", s.proxyAddr)
	}
	return port
}

// libpq is the reference client. Nearly every PostgreSQL tool is built on it,
// so if Pontus's SCRAM exchange is wrong in a way pgx tolerates, this is where
// it shows.
func TestLibpqAuthenticatesAgainstPontus(t *testing.T) {
	runtimeBin := containerRuntime(t)

	s := startAuthStackOnAllInterfaces(t)
	port := proxyPort(t, s)

	// The container reaches the host on its default gateway.
	// Ask the primary rather than the replica: the replica is rebuilt by the
	// cluster script and may be mid-restart.
	gw, err := exec.Command(runtimeBin, "exec", primaryContainer(), "sh", "-c",
		`ip route | awk '/^default/{print $3}'`).Output()
	if err != nil {
		t.Skipf("cannot find the host gateway from a container: %v", err)
	}
	host := strings.TrimSpace(string(gw))
	if host == "" {
		t.Skip("no default gateway inside the container")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, runtimeBin, "exec",
		"-e", "PGPASSWORD="+backendPass(),
		primaryContainer(), "psql",
		"-h", host, "-p", port,
		"-U", backendUser(), "-d", backendDB(),
		"-tAc", "SELECT 'libpq-ok', current_user").CombinedOutput()

	if err != nil {
		t.Fatalf("libpq could not authenticate against Pontus: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "libpq-ok") {
		t.Fatalf("unexpected reply from libpq: %s", out)
	}
	t.Logf("libpq: %s", strings.TrimSpace(string(out)))
}

// asyncpg implements SCRAM in pure Python, independently of libpq and of pgx.
// Agreeing with two implementations that share no code is what makes the
// exchange credible.
func TestAsyncpgAuthenticatesAgainstPontus(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not installed; cannot fetch asyncpg")
	}

	s := startAuthStackOnAllInterfaces(t)
	port := proxyPort(t, s)

	script := fmt.Sprintf(`
import asyncio, asyncpg

async def main():
    conn = await asyncpg.connect(
        host="127.0.0.1", port=%s, user=%q, password=%q, database=%q)
    # More than one statement: the session has to survive past its first.
    for i in range(5):
        assert await conn.fetchval("SELECT $1::int", i) == i
    who = await conn.fetchval("SELECT current_user")
    await conn.close()
    print("asyncpg-ok", who)

asyncio.run(main())
`, port, backendUser(), backendPass(), backendDB())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "uv", "run", "--quiet",
		"--with", "asyncpg", "python", "-c", script).CombinedOutput()
	if err != nil {
		// Fetching asyncpg needs the network. A machine that cannot reach the
		// index has not told us anything about Pontus, so skip rather than fail
		// — but never skip on a reply that came back wrong.
		if ctx.Err() != nil || strings.Contains(string(out), "Failed to fetch") ||
			strings.Contains(string(out), "No solution found") {
			t.Skipf("could not fetch asyncpg: %v", err)
		}
		t.Fatalf("asyncpg could not authenticate against Pontus: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "asyncpg-ok") {
		t.Fatalf("unexpected reply from asyncpg: %s", out)
	}
	t.Logf("asyncpg: %s", strings.TrimSpace(string(out)))
}

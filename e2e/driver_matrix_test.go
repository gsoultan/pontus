//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
//
// This has been observed passing. It currently skips on some machines because
// Apple's `container exec` returns 125 without running anything when invoked
// from Go, while the identical command succeeds from a shell:
//
//	container exec -e PGPASSWORD=... pontus-e2e-primary sh -c \
//	  'GW=$(ip route | awk "/^default/{print \$3}"); \
//	   exec psql -h "$GW" -p PORT -U postgres -d postgres -tAc "SELECT 1"'
//
// If it skips, run that by hand before assuming libpq is fine — a skip here is
// an untested claim, not a passing one.
func TestLibpqAuthenticatesAgainstPontus(t *testing.T) {
	runtimeBin := containerRuntime(t)

	s := startAuthStackOnAllInterfaces(t)
	port := proxyPort(t, s)

	// The container reaches the host on its default gateway.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// One exec, not two, and every plausible route to the host tried in turn.
	//
	// How a container reaches its host is runtime-specific: rootless Podman
	// publishes host.containers.internal and does *not* route to host services
	// via the default gateway, Docker Desktop uses host.docker.internal, and
	// Apple's runtime routes via the gateway. Assuming one produced a
	// "connection refused" that looked like Pontus not listening.
	script := fmt.Sprintf(
		`for H in host.containers.internal host.docker.internal `+
			`$(ip route 2>/dev/null | awk '/^default/{print $3}'); do `+
			`  if psql -h "$H" -p %s -U %s -d %s -tAc "SELECT 'libpq-ok', current_user" 2>/dev/null; then `+
			`    exit 0; `+
			`  fi; `+
			`done; `+
			`echo "no route from this container to the proxy" >&2; exit 3`,
		port, backendUser(), backendDB())

	out, err := exec.CommandContext(ctx, runtimeBin, "exec",
		"-e", "PGPASSWORD="+backendPass(),
		primaryContainer(), "sh", "-c", script).CombinedOutput()

	if err != nil {
		// The distinction that makes this skip trustworthy: psql writes to
		// stderr for every failure it can have — a refused password, an
		// unreachable host, a protocol error. Empty output means it never ran,
		// which is the container runtime declining to start it and says nothing
		// about Pontus.
		//
		// Anything psql actually said is a result, and is reported as a failure.
		if len(bytes.TrimSpace(out)) == 0 {
			t.Skipf("the container runtime would not start psql (%v); "+
				"this says nothing about Pontus. The same command works from a "+
				"shell — see the comment above this test", err)
		}
		t.Fatalf("libpq could not authenticate against Pontus: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "libpq-ok") {
		t.Fatalf("unexpected reply from libpq: %s", out)
	}
	t.Logf("libpq: %s", strings.TrimSpace(string(out)))
}

// pythonWithAsyncpg finds an interpreter that can already import asyncpg.
//
// Deliberately never installs anything. An earlier version resolved the package
// on demand with `uv run --with`, which spent longer than the suite's budget and
// then left Go blocked on a pipe inherited by the killed child — the suite hung
// for four hundred seconds. A test that hangs is worse than no test, and a test
// that needs the network is not testing Pontus.
func pythonWithAsyncpg(t *testing.T) string {
	t.Helper()

	candidates := []string{
		os.Getenv("PONTUS_E2E_PYTHON"),
		"/tmp/pontus-drivers/bin/python",
		"python3",
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = exec.CommandContext(ctx, path, "-c", "import asyncpg").Run()
		cancel()
		if err == nil {
			return path
		}
	}

	t.Skip("no interpreter with asyncpg. Create one with:\n" +
		"  uv venv /tmp/pontus-drivers --python 3.12 && \\\n" +
		"  uv pip install --python /tmp/pontus-drivers/bin/python asyncpg\n" +
		"or point PONTUS_E2E_PYTHON at one")
	return ""
}

// asyncpg implements SCRAM in pure Python, sharing no code with libpq or with
// pgx. Three implementations that agree, written independently, is what makes a
// wire exchange credible — one that agrees with itself proves only that it is
// self-consistent.
//
// This was finding A10 for a while. asyncpg connected and then hung on its
// first query, because it prepares with a Flush rather than a Sync and Pontus
// decided a reply was over only when it saw ReadyForQuery — which a Flush never
// produces. pgx and libpq happened not to exercise that path, which is exactly
// why one driver proves nothing.
func TestAsyncpgAuthenticatesAgainstPontus(t *testing.T) {

	python := pythonWithAsyncpg(t)

	s := startAuthStackOnAllInterfaces(t)
	port := proxyPort(t, s)

	script := fmt.Sprintf(`
import asyncio, asyncpg

async def main():
    conn = await asyncpg.connect(
        host="127.0.0.1", port=%s, user=%q, password=%q, database=%q)
    # More than one statement: the session has to survive past its first, which
    # is where a connection that authenticated but cannot be reused shows up.
    for i in range(5):
        assert await conn.fetchval("SELECT $1::int", i) == i
    who = await conn.fetchval("SELECT current_user")
    await conn.close()
    print("asyncpg-ok", who)

asyncio.run(main())
`, port, backendUser(), backendPass(), backendDB())

	// Short, because the observed failure is a hang. A test that waits a minute
	// to tell you something is broken is a test people stop running.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("asyncpg could not authenticate against Pontus: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "asyncpg-ok") {
		t.Fatalf("unexpected reply from asyncpg: %s", out)
	}
	t.Logf("asyncpg: %s", strings.TrimSpace(string(out)))
}

// A wrong password must be refused for asyncpg exactly as it is for the others.
func TestAsyncpgRejectsAWrongPassword(t *testing.T) {
	python := pythonWithAsyncpg(t)

	s := startAuthStackOnAllInterfaces(t)
	port := proxyPort(t, s)

	script := fmt.Sprintf(`
import asyncio, asyncpg

async def main():
    try:
        await asyncpg.connect(host="127.0.0.1", port=%s, user=%q,
                              password="wrong-password", database=%q)
    except Exception as exc:
        print("asyncpg-refused", type(exc).__name__)
        return
    print("asyncpg-ACCEPTED")

asyncio.run(main())
`, port, backendUser(), backendDB())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, _ := exec.CommandContext(ctx, python, "-c", script).CombinedOutput()
	if strings.Contains(string(out), "asyncpg-ACCEPTED") {
		t.Fatalf("asyncpg was let in with a wrong password: %s", out)
	}
	if !strings.Contains(string(out), "asyncpg-refused") {
		t.Fatalf("asyncpg neither refused nor connected: %s", out)
	}
	t.Logf("asyncpg: %s", strings.TrimSpace(string(out)))
}

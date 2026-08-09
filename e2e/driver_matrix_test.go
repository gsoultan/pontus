//go:build e2e

package e2e

import (
	"bytes"
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

	// One exec, not two. Finding the gateway in a separate call doubled the
	// chances of a runtime hiccup, and a skip from that is indistinguishable
	// from a real failure — which makes the test worse than not having it.
	// The gateway is resolved inside the same shell that runs psql.
	script := fmt.Sprintf(
		`GW=$(ip route | awk '/^default/{print $3}'); `+
			`test -n "$GW" || { echo "no default gateway" >&2; exit 3; }; `+
			`exec psql -h "$GW" -p %s -U %s -d %s -tAc "SELECT 'libpq-ok', current_user"`,
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

// asyncpg is deliberately absent.
//
// It implements SCRAM in pure Python, independently of libpq and of pgx, which
// makes it the most valuable third opinion available — but fetching it with uv
// compiles from source and takes longer than the suite's budget, and killing
// the child leaves Go blocked on an inherited pipe. The result was a test that
// hung for four hundred seconds, which is worse than no test at all.
//
// To add it: preinstall asyncpg in the environment and invoke the interpreter
// directly, rather than resolving a package on demand. The assertion wanted is
// a multi-statement session, so the connection has to survive past its first
// query rather than merely authenticate.

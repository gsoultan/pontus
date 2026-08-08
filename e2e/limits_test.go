//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

// A configured rate limit must actually slow traffic down.
//
// The limiter was enabled in every generated config and never verified. It ran
// at a hardcoded 100rps that ignored configuration for a long time, so
// "enabled: true" told an operator nothing about what was being enforced.
func TestRateLimitThrottlesTraffic(t *testing.T) {
	// Deliberately tight. Cost is estimated per statement, so a plain SELECT
	// spends more than one token and a handful of queries has to wait.
	s := startStackWith(t, func(cfg string) string {
		return setRateLimit(cfg, 4, 4)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)

	const queries = 12
	start := time.Now()
	for i := range queries {
		var n int
		if err := conn.QueryRow(ctx, "SELECT $1::int", i).Scan(&n); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)

	// Unthrottled these complete in well under a second against a local
	// database. At 4 tokens per second with a burst of 4, twelve statements
	// cannot.
	if elapsed < 2*time.Second {
		t.Errorf("12 queries took %v at 4rps; the rate limit is not being enforced",
			elapsed.Round(time.Millisecond))
	}
}

// The same traffic must not be throttled when the limit is generous, so the
// previous test is measuring the limiter rather than the database.
func TestRateLimitDoesNotThrottleBelowTheLimit(t *testing.T) {
	s := startStack(t) // 500rps / 1000 burst

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)

	start := time.Now()
	for i := range 12 {
		var n int
		if err := conn.QueryRow(ctx, "SELECT $1::int", i).Scan(&n); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("12 queries took %v well under the limit; something other than the "+
			"limiter is slowing traffic", elapsed.Round(time.Millisecond))
	}
}

// Session pooling must hold the backend connection for the client's session,
// where transaction pooling returns it between statements.
//
// pooling_mode was configurable, offered in the dashboard, and read by nothing
// for a long time — selecting "session" silently gave transaction pooling. The
// difference is observable: an idle session holds a connection or it does not.
func TestSessionPoolingHoldsTheConnection(t *testing.T) {
	activeWhileIdle := func(t *testing.T, mode string) float64 {
		t.Helper()

		s := startStackWith(t, func(cfg string) string {
			return setYAMLScalar(cfg, "pooling_mode", mode)
		})

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		conn := connect(t, ctx, s)
		var n int
		if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
			t.Fatalf("%s: query: %v", mode, err)
		}

		// An idle session either still holds its backend connection or it does
		// not; that is the whole difference between the two modes.
		time.Sleep(time.Second)

		token := s.login()
		projectID, proxyID := s.project(token)
		out, code := s.rpc("GetStatus",
			map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
		if code != http.StatusOK {
			t.Fatalf("%s: GetStatus failed (%d): %v", mode, code, out)
		}

		var active float64
		for _, raw := range asSlice(out["backends"]) {
			if backend, ok := raw.(map[string]any); ok {
				active += asFloat(backend["activeConns"])
			}
		}
		return active
	}

	transaction := activeWhileIdle(t, "transaction")
	session := activeWhileIdle(t, "session")

	t.Logf("active connections while idle: transaction=%v session=%v", transaction, session)

	if session <= transaction {
		t.Errorf("session pooling held %v connections while idle and transaction pooling held %v; "+
			"the modes are behaving identically", session, transaction)
	}
}

// A pool with no capacity left must refuse promptly rather than hang.
//
// Whatever the ceiling is, exceeding it has to produce an error the client can
// act on — a proxy that stops answering is worse than one that says no.
func TestPoolExhaustionFailsRatherThanHangs(t *testing.T) {
	s := startStackWith(t, func(cfg string) string {
		// One connection, held by a session that never becomes idle.
		cfg = setYAMLScalar(cfg, "max_conns", "1")
		cfg = setYAMLScalar(cfg, "min_idle", "0")
		return setYAMLScalar(cfg, "pooling_mode", "session")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Hold the only connection.
	holder := connect(t, ctx, s)
	var n int
	if err := holder.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("holder query: %v", err)
	}

	// A second session competes for a pool that has nothing to give.
	var wg sync.WaitGroup
	var elapsed time.Duration
	var queryErr error

	wg.Go(func() {
		attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer attemptCancel()

		start := time.Now()
		conn, err := connectErr(attemptCtx, s)
		if err == nil {
			var m int
			queryErr = conn.QueryRow(attemptCtx, "SELECT 2").Scan(&m)
			_ = conn.Close(context.Background())
		} else {
			queryErr = err
		}
		elapsed = time.Since(start)
	})
	wg.Wait()

	t.Logf("second session resolved in %v with err=%v", elapsed.Round(time.Millisecond), queryErr)

	// Either it was served (the pool freed up) or it was refused. What it must
	// not do is hang until the client's own deadline.
	if elapsed >= 40*time.Second {
		t.Errorf("a second session hung for %v against an exhausted pool; "+
			"exhaustion must surface as an error", elapsed.Round(time.Second))
	}
}

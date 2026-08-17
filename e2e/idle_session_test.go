//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A session that sits idle longer than query_timeout must still work.
//
// The timeout context was created before the read that waits for the client, so
// it counted while nothing was happening: a connection held idle past
// query_timeout got an already-expired deadline for its next statement and
// failed instantly. Connection-pooling clients hold connections idle by design,
// which is most of them.
//
// query_timeout is lowered so the test measures the behaviour rather than
// waiting out the default.
func TestIdleSessionSurvivesTheQueryTimeout(t *testing.T) {
	requireBackend(t)

	s := startStackWith(t, func(cfg string) string {
		return setYAMLScalar(cfg, "query_timeout", "3s")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	var n int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("first query: %v", err)
	}

	// Idle for longer than query_timeout.
	time.Sleep(5 * time.Second)

	if err := conn.QueryRow(ctx, "SELECT 2").Scan(&n); err != nil {
		t.Fatalf("a query after %v idle failed: %v\n"+
			"the query timeout was counting while the session was doing nothing",
			5*time.Second, err)
	}
	if n != 2 {
		t.Fatalf("SELECT 2 returned %d", n)
	}

	// And again, so the fix is not a one-off.
	time.Sleep(4 * time.Second)
	if err := conn.QueryRow(ctx, "SELECT 3").Scan(&n); err != nil || n != 3 {
		t.Fatalf("second idle period: %v (n=%d)", err, n)
	}
}

// A genuinely slow statement must still be cut off, or the fix would have
// removed the protection instead of correcting it.
func TestSlowQueryStillHitsTheTimeout(t *testing.T) {
	requireBackend(t)

	s := startStackWith(t, func(cfg string) string {
		return setYAMLScalar(cfg, "query_timeout", "3s")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	start := time.Now()
	var slept string
	err := conn.QueryRow(ctx, "SELECT pg_sleep(20)").Scan(&slept)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a twenty-second statement completed under a three-second timeout")
	}
	if elapsed > 15*time.Second {
		t.Errorf("the timeout took %v to fire; it is no longer bounding anything",
			elapsed.Round(time.Second))
	}
	// The error has to name the cause. A bare "conn closed" is what a dead
	// database looks like too, so it sends the operator to the wrong place —
	// Pontus has to say in the protocol that *it* stopped the statement.
	if !strings.Contains(err.Error(), "query_timeout") {
		t.Errorf("client saw %q, which does not identify Pontus's timeout as the cause", err)
	}
	t.Logf("slow query cut off after %v: %v", elapsed.Round(time.Millisecond), err)
}

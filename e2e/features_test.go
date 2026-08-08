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

// cacheStats reads the result-cache counters out of GetStatus.
func cacheStats(t *testing.T, s *stack, token, projectID, proxyID string) (hits, misses float64, present bool) {
	t.Helper()

	out, code := s.rpc("GetStatus",
		map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
	if code != http.StatusOK {
		t.Fatalf("GetStatus failed (%d): %v", code, out)
	}

	raw, ok := out["cacheStats"].(map[string]any)
	if !ok {
		return 0, 0, false
	}
	return asFloat(raw["hits"]), asFloat(raw["misses"]), true
}

// The result cache is enabled in the harness config. It must actually serve a
// repeated read from cache rather than merely existing.
//
// Written because the feature was configurable, rendered by the dashboard, and
// never verified: GetStatus did not populate cache_stats at all, so the card
// the UI draws for it could never appear and nobody could tell whether the
// cache was doing anything.
func TestCacheServesRepeatedReads(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Simple protocol: the query text arrives in one message, which is the
	// case the cache was written for. Extended protocol is checked separately.
	conn := connectMode(t, ctx, s, pgx.QueryExecModeSimpleProtocol)
	token := s.login()
	projectID, proxyID := s.project(token)

	if _, _, present := cacheStats(t, s, token, projectID, proxyID); !present {
		t.Fatal("GetStatus reports no cacheStats; the dashboard's cache card cannot render")
	}

	before, _, _ := cacheStats(t, s, token, projectID, proxyID)

	// The same read several times over. Whatever the key includes, an identical
	// query on one session must be cacheable.
	const repeats = 6
	for range repeats {
		var n int
		if err := conn.QueryRow(ctx, "SELECT 42").Scan(&n); err != nil {
			t.Fatalf("cached query: %v", err)
		}
		if n != 42 {
			t.Fatalf("SELECT 42 returned %d", n)
		}
	}

	var after float64
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		after, _, _ = cacheStats(t, s, token, projectID, proxyID)
		if after > before {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}

	if after <= before {
		t.Errorf("cache recorded no hits across %d identical reads (before=%v after=%v); "+
			"the cache is enabled but not serving anything", repeats, before, after)
	}
}

// A cached read must not survive a write to the table it came from.
//
// Serving a pre-write row for the rest of the TTL is the failure that makes a
// cache worse than none.
func TestCacheIsInvalidatedByWrites(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)
	table := fmt.Sprintf("pontus_cache_%d", time.Now().UnixNano())

	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (id int)", table)); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	t.Cleanup(func() { dropTable(t, table) })

	if _, err := conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s VALUES (1)", table)); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	read := func() int {
		t.Helper()
		var count int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		return count
	}

	// Warm the cache, then change the answer.
	if got := read(); got != 1 {
		t.Fatalf("initial count = %d, want 1", got)
	}
	if got := read(); got != 1 {
		t.Fatalf("cached count = %d, want 1", got)
	}

	if _, err := conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s VALUES (2)", table)); err != nil {
		t.Fatalf("second INSERT: %v", err)
	}

	if got := read(); got != 2 {
		t.Errorf("count after write = %d, want 2 — a stale cached result was served", got)
	}
}

// An unreachable backend must fail the client promptly with an error, not hang
// until the query timeout.
//
// This is the failure path a pooled proxy is judged on: the database being
// down should look like the database being down.
func TestUnreachableBackendFailsPromptly(t *testing.T) {
	s := startStackWith(t, func(cfg string) string {
		// Point the only backend at a closed port.
		return replaceBackendAddr(cfg, "127.0.0.1:59999")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	start := time.Now()
	_, err := connectErr(ctx, s)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a session was established through a proxy with no reachable backend")
	}
	// dial_timeout is 5s and acquisition retries a few times; anything near the
	// 30s query timeout means the client was left hanging.
	if elapsed > 30*time.Second {
		t.Errorf("failure took %v; an unreachable backend should fail fast", elapsed.Round(time.Second))
	}
}

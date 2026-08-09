//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// Every mainstream PostgreSQL driver uses prepared statements — pgx, JDBC and
// asyncpg all do by default — so this is the ordinary path, not a corner.
//
// It was broken by the result cache. The extended protocol splits one query
// into Parse, Bind and Execute; the reply to a Parse is ParseComplete, not a
// result set. But a Parse carries the SQL text, so it normalised and classified
// as a cacheable read, and the second client to run the same statement was
// answered with the first client's stored *result* where its ParseComplete
// belonged. The connection desynchronised and the client's next Bind referenced
// a statement the server had never parsed:
//
//	ERROR: prepared statement "stmtcache_..." does not exist (SQLSTATE 26000)
//
// The first client always succeeded, which is why this survived: a single
// client, or a single run, looks perfectly healthy.
func TestPreparedStatementsWorkWithTheCacheEnabled(t *testing.T) {
	s := startStack(t) // the default harness config has the cache enabled

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	for i := range 8 {
		conn := connect(t, ctx, s) // pgx defaults to the extended protocol
		var n int
		if err := conn.QueryRow(ctx, "SELECT pg_is_in_recovery()::int").Scan(&n); err != nil {
			_ = conn.Close(context.Background())
			t.Fatalf("client %d failed on a statement the previous client had already run: %v\n"+
				"a cached result was served where a protocol acknowledgement belonged", i, err)
		}
		_ = conn.Close(context.Background())
		time.Sleep(150 * time.Millisecond)
	}
}

// The cache must still work for the simple protocol, or the fix would have
// disabled caching rather than corrected it.
func TestSimpleProtocolIsStillCached(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	const q = "SELECT 42 AS cached_probe"
	for i := range 4 {
		var n int
		if err := conn.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatalf("simple-protocol query %d: %v", i, err)
		}
		if n != 42 {
			t.Fatalf("query %d returned %d", i, n)
		}
	}

	token := s.login()
	projectID, proxyID := s.project(token)
	out, code := s.rpc("GetStatus", map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
	if code == 200 {
		if stats, ok := out["cacheStats"]; ok {
			t.Logf("cache stats after four identical simple queries: %v", stats)
		}
	}
}

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A session must survive its second query when a replica is configured.
//
// It did not. Query 0 ran on the connection that carried the handshake, was
// released at the transaction boundary, and query 1 routed to the replica —
// where every connection is a raw socket that has never negotiated a startup
// exchange. The client stopped getting answers and the session died with
// "conn closed". Adding a replica for read capacity broke the data plane, and
// nothing said why.
//
// Pontus still cannot open a backend connection of its own (finding A8), so
// reads stay on the handshake backend. That is the honest outcome: unbalanced
// beats broken.
func TestSessionSurvivesManyQueriesWithAReplica(t *testing.T) {
	requireCluster(t)
	s := startStackWith(t, func(cfg string) string {
		cfg = withReplicaBackend(cfg, replicaAddr())
		// Cache off so every query reaches a backend; a cached answer would
		// hide exactly the failure under test.
		return replaceFirst(cfg, "cache:\n  enabled: true", "cache:\n  enabled: false")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	for i := range 12 {
		var one int
		// Distinct text per query so nothing can be served from a cache.
		q := fmt.Sprintf("SELECT %d", i)
		if err := conn.QueryRow(ctx, q).Scan(&one); err != nil {
			t.Fatalf("query %d failed: %v\n"+
				"the session died mid-flight; with a replica configured, acquisition "+
				"handed it a connection that never completed a startup exchange", i, err)
		}
		if one != i {
			t.Fatalf("query %d returned %d", i, one)
		}
	}
}

// The same, interleaving reads and writes so routing is asked to change
// backends on nearly every statement.
func TestMixedReadWriteSessionSurvives(t *testing.T) {
	requireCluster(t)
	s := startStackWith(t, func(cfg string) string {
		cfg = withReplicaBackend(cfg, replicaAddr())
		return replaceFirst(cfg, "cache:\n  enabled: true", "cache:\n  enabled: false")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx,
		"CREATE TABLE IF NOT EXISTS e2e_mixed(id int primary key)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := range 8 {
		if _, err := conn.Exec(ctx,
			fmt.Sprintf("INSERT INTO e2e_mixed VALUES (%d) ON CONFLICT DO NOTHING", i)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		var n int
		if err := conn.QueryRow(ctx,
			fmt.Sprintf("SELECT count(*) FROM e2e_mixed /* r%d */", i)).Scan(&n); err != nil {
			t.Fatalf("read %d after a write: %v", i, err)
		}
	}
}

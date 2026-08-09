//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Pontus is a connection pooler. This is the test that says whether that is
// true, and until now it was not: the client's Terminate was forwarded to the
// backend, so every client session ended its backend connection and the next
// client paid for a fresh one. Four sequential sessions produced four backend
// PIDs.
//
// Reuse needs three things, all of which had to be true at once: the goodbye is
// not forwarded, the connection carries the identity it authenticated as so it
// is only offered to the same user, and a reused connection is not given a
// second startup exchange — the backend is past that phase and would read a
// StartupMessage as a malformed command.
func TestBackendConnectionsAreReusedAcrossClients(t *testing.T) {
	s := authStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	starts := map[string]int{}
	for i := range 8 {
		conn, err := connectAs(t, ctx, s, backendUser(), backendPass())
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}

		var pid int
		var started string
		// Distinct text per query so no cache can answer it — a cached row would
		// make every client look like the first one, which is exactly how an
		// earlier version of this measurement fooled me.
		q := fmt.Sprintf("SELECT pg_backend_pid(), backend_start::text "+
			"FROM pg_stat_activity WHERE pid = pg_backend_pid() /* client %d */", i)
		if err := conn.QueryRow(ctx, q).Scan(&pid, &started); err != nil {
			_ = conn.Close(context.Background())
			t.Fatalf("client %d query: %v", i, err)
		}
		starts[started]++
		t.Logf("client %d served by pid %d (started %s)", i, pid, started)

		_ = conn.Close(context.Background())
		time.Sleep(250 * time.Millisecond)
	}

	if len(starts) == 8 {
		t.Fatal("eight clients used eight distinct backend connections; nothing was pooled")
	}
	t.Logf("8 clients shared %d backend connection(s)", len(starts))
}

// Reuse must not leak one client's session state to the next.
func TestReusedConnectionDoesNotCarryStateForward(t *testing.T) {
	s := authStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	first, err := connectAs(t, ctx, s, backendUser(), backendPass())
	if err != nil {
		t.Fatalf("first client: %v", err)
	}
	if _, err := first.Exec(ctx, "SET application_name = 'tenant-one'"); err != nil {
		t.Fatalf("set: %v", err)
	}
	_ = first.Close(context.Background())

	for i := range 4 {
		second, err := connectAs(t, ctx, s, backendUser(), backendPass())
		if err != nil {
			t.Fatalf("second client %d: %v", i, err)
		}
		var name string
		err = second.QueryRow(ctx,
			fmt.Sprintf("SHOW application_name /* probe %d */", i)).Scan(&name)
		_ = second.Close(context.Background())
		if err != nil {
			t.Fatalf("second client %d: %v", i, err)
		}
		if name == "tenant-one" {
			t.Fatalf("client %d inherited the previous client's application_name; "+
				"a reused connection must not carry session state forward", i)
		}
	}
}

//go:build e2e

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A simple-query message may carry several statements. The backend answers with
// a result per statement and a *single* ReadyForQuery at the end, so a proxy
// that stops at the first CommandComplete leaves the rest of the reply in the
// socket for whoever borrows the connection next.
func TestMultiStatementSimpleQuery(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	// Exec with no arguments uses the simple protocol, so this is one message.
	if _, err := conn.Exec(ctx,
		"CREATE TEMP TABLE multi (id int); INSERT INTO multi VALUES (1); INSERT INTO multi VALUES (2)"); err != nil {
		t.Fatalf("a three-statement simple query failed: %v", err)
	}

	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM multi").Scan(&n); err != nil {
		t.Fatalf("the session was unusable afterwards: %v", err)
	}
	if n != 2 {
		t.Errorf("%d rows after a multi-statement insert, want 2", n)
	}
}

// An error inside a transaction leaves it aborted: the backend answers every
// further statement with 25P02 until ROLLBACK, and reports 'E' rather than 'I'
// in ReadyForQuery. A connection released in that state carries an open aborted
// transaction to whoever borrows it next.
func TestAbortedTransactionRecovers(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	if _, err := tx.Exec(ctx, "SELECT 1/0"); err == nil {
		t.Fatal("division by zero was accepted")
	}

	// The transaction is aborted; the next statement must be refused.
	if _, err := tx.Exec(ctx, "SELECT 1"); err == nil {
		t.Error("a statement ran inside an aborted transaction")
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Errorf("the session did not recover after rollback: %d, %v", one, err)
	}
}

// An empty statement gets EmptyQueryResponse instead of CommandComplete. A
// reply scanner that only knows about CommandComplete waits for a terminator
// that never comes.
func TestEmptyQuery(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, ""); err != nil {
		t.Errorf("an empty statement failed: %v", err)
	}

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Errorf("the session was unusable after an empty statement: %d, %v", one, err)
	}
}

// Request collapsing serves one backend reply to several clients. It is gated on
// the statement being read-only and outside a transaction — but the extended
// protocol's Parse/Bind/Execute is a *sequence*, and two clients sending
// byte-identical sequences are still preparing statements on two different
// connections. Sharing one reply between them is only safe if the reply really
// is interchangeable.
func TestConcurrentIdenticalPreparedQueries(t *testing.T) {
	requireBackend(t)

	s := startStack(t) // the default harness config has the cache enabled
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const clients = 8
	var wg sync.WaitGroup
	errs := make(chan error, clients*4)

	for i := range clients {
		wg.Go(func() {
			conn := connectSimple(t, ctx, s)
			defer conn.Close(context.Background())

			for round := range 4 {
				// Same SQL, different argument: each client must get its own
				// answer, not a neighbour's.
				want := i*100 + round
				var got int
				if err := conn.QueryRow(ctx, "SELECT $1::int", want).Scan(&got); err != nil {
					errs <- err
					return
				}
				if got != want {
					errs <- &mismatch{want: want, got: got}
					return
				}
			}
		})
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

type mismatch struct{ want, got int }

func (m *mismatch) Error() string {
	return "a client was served another's rows: wanted " +
		itoa(m.want) + ", got " + itoa(m.got)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

// A result larger than the capture bound must still reach the client in full.
// Only the *remembering* stops when the bound is passed; the streaming does not.
func TestResultLargerThanTheCaptureBound(t *testing.T) {
	requireBackend(t)

	s := startStack(t) // the default harness config has the cache enabled
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	// ~20 MB of text, comfortably past the 8 MiB default bound.
	rows, err := conn.Query(ctx,
		"SELECT g, repeat('z', 1000) FROM generate_series(1, 20000) g")
	if err != nil {
		t.Fatalf("a large query failed: %v", err)
	}

	count, total := 0, 0
	for rows.Next() {
		var id int
		var payload string
		if err := rows.Scan(&id, &payload); err != nil {
			t.Fatalf("scan failed at row %d: %v", count, err)
		}
		count++
		total += len(payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("the large result was truncated: %v", err)
	}
	if count != 20000 {
		t.Errorf("received %d rows, want 20000", count)
	}
	if total != 20000*1000 {
		t.Errorf("received %d payload bytes, want %d", total, 20000*1000)
	}

	// And the session still works.
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Errorf("the session was unusable after a large result: %d, %v", one, err)
	}
}

// A cursor fetched in batches uses Execute with a row limit, which the backend
// answers with PortalSuspended rather than CommandComplete.
func TestCursorFetchedInBatches(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx,
		"DECLARE batched CURSOR FOR SELECT g FROM generate_series(1, 5000) g"); err != nil {
		t.Fatalf("DECLARE CURSOR failed: %v", err)
	}

	seen := 0
	for {
		rows, err := tx.Query(ctx, "FETCH 250 FROM batched")
		if err != nil {
			t.Fatalf("FETCH failed after %d rows: %v", seen, err)
		}
		batch := 0
		for rows.Next() {
			var g int
			if err := rows.Scan(&g); err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			seen++
			batch++
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("batch read failed after %d rows: %v", seen, err)
		}
		if batch == 0 {
			break
		}
	}

	if seen != 5000 {
		t.Errorf("fetched %d rows through a cursor, want 5000", seen)
	}
}

// pgx's simple-protocol mode sends everything as one Query message, which is a
// different path through the classifier and the cache than the default.
func TestSimpleProtocolMode(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	for i := range 20 {
		var got int
		err := conn.QueryRow(ctx, "SELECT $1::int", pgx.QueryExecModeSimpleProtocol, i).Scan(&got)
		if err != nil {
			t.Fatalf("simple-protocol query %d failed: %v", i, err)
		}
		if got != i {
			t.Fatalf("simple-protocol query %d returned %d", i, got)
		}
	}
}

// A table created after a query failed against it must become visible.
//
// This is the invalidation path rather than the error-cache path: CREATE TABLE
// names the table, so it evicts anything keyed to it, and the entry would be
// dropped whether or not failures are cached. Worth having end to end — an
// earlier version of this test claimed to prove that failures are not cached
// and passed with that guard removed, which is why the guard itself is proved
// in server/proxy/middleware/cache_failure_test.go instead.
func TestATableCreatedAfterAFailedQueryBecomesVisible(t *testing.T) {
	requireBackend(t)

	s := startStack(t) // the default harness config has the cache enabled
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	// Unique per run so a previous run cannot have cached anything.
	table := "late_arrival_" + itoa(int(time.Now().UnixNano()%1_000_000))
	query := "SELECT count(*) FROM " + table

	// Fails: the table does not exist yet. Simple protocol, so it is cacheable.
	if _, err := conn.Exec(ctx, query); err == nil {
		t.Fatalf("querying a table that does not exist succeeded")
	}

	if _, err := conn.Exec(ctx, "CREATE TABLE "+table+" (id int)"); err != nil {
		t.Fatalf("could not create the table: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_, _ = conn.Exec(cctx, "DROP TABLE IF EXISTS "+table)
	})

	var n int
	if err := conn.QueryRow(ctx, query, pgx.QueryExecModeSimpleProtocol).Scan(&n); err != nil {
		t.Fatalf("the table was created but the query still fails: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d on an empty table", n)
	}
}

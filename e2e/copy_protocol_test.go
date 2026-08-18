//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// COPY is its own message flow. The backend answers with CopyInResponse and
// then *waits for the client*, so a proxy that forwards a request and then
// reads the backend until the reply ends has nothing to wait for — the client's
// data is still on the other side of it.
//
// This is not exotic: \copy, pg_restore and every bulk loader use it.
func TestCopyFromStdinWorks(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	table := "copy_probe"
	if _, err := conn.Exec(ctx, "CREATE TEMP TABLE "+table+" (id int, note text)"); err != nil {
		t.Fatalf("could not create the target table: %v", err)
	}

	rows := make([][]any, 0, 500)
	for i := range 500 {
		rows = append(rows, []any{i, fmt.Sprintf("row-%d", i)})
	}

	copied, err := conn.CopyFrom(ctx, pgx.Identifier{table}, []string{"id", "note"},
		pgx.CopyFromRows(rows))
	if err != nil {
		t.Fatalf("COPY FROM STDIN failed: %v", err)
	}
	if copied != int64(len(rows)) {
		t.Errorf("copied %d rows, sent %d", copied, len(rows))
	}

	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("could not count what was copied: %v", err)
	}
	if count != len(rows) {
		t.Errorf("%d rows landed in the table, copied %d", count, len(rows))
	}
}

// COPY TO STDOUT is the other direction, and large enough here to cross the
// read buffer several times.
func TestCopyToStdoutWorks(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	var out strings.Builder
	tag, err := conn.PgConn().CopyTo(ctx, &out,
		"COPY (SELECT g, repeat('x', 200) FROM generate_series(1, 2000) g) TO STDOUT")
	if err != nil {
		t.Fatalf("COPY TO STDOUT failed: %v", err)
	}
	if !strings.HasPrefix(tag.String(), "COPY") {
		t.Errorf("command tag was %q", tag.String())
	}

	if got := strings.Count(out.String(), "\n"); got != 2000 {
		t.Errorf("received %d lines, expected 2000", got)
	}
}

// A COPY the backend rejects part-way is the case a sequential relay deadlocks
// on: the backend stops reading and answers with an error, the client keeps
// sending, the backend's receive buffer fills, and the proxy's write blocks
// with that error sitting unread on the other socket.
func TestCopyRejectedMidStreamStillReturns(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx,
		"CREATE TEMP TABLE copy_reject (id int primary key)"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Every row the same key, so the backend rejects once the first duplicate
	// arrives rather than at the end.
	rows := make([][]any, 0, 20000)
	for range 20000 {
		rows = append(rows, []any{1})
	}

	done := make(chan error, 1)
	go func() {
		_, err := conn.CopyFrom(ctx, pgx.Identifier{"copy_reject"}, []string{"id"},
			pgx.CopyFromRows(rows))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a COPY of 20000 duplicate keys was accepted")
		}
		t.Logf("rejected as expected: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("a rejected COPY never returned — the relay deadlocked")
	}
}

// A COPY large enough to cross the read buffer many times, and a session that
// still works afterwards. A relay that read past CopyDone would leave the next
// statement's bytes behind it.
func TestLargeCopyAndThenTheSessionContinues(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx,
		"CREATE TEMP TABLE copy_big (id int, payload text)"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	const rowCount = 50000
	payload := strings.Repeat("p", 200)
	rows := make([][]any, 0, rowCount)
	for i := range rowCount {
		rows = append(rows, []any{i, payload})
	}

	copied, err := conn.CopyFrom(ctx, pgx.Identifier{"copy_big"},
		[]string{"id", "payload"}, pgx.CopyFromRows(rows))
	if err != nil {
		t.Fatalf("a ~10 MB COPY failed: %v", err)
	}
	if copied != rowCount {
		t.Errorf("copied %d rows, sent %d", copied, rowCount)
	}

	// The session has to be usable afterwards. If the relay had over-read, this
	// statement's opening bytes went to the backend as COPY data.
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM copy_big").Scan(&n); err != nil {
		t.Fatalf("the session was unusable after the COPY: %v", err)
	}
	if n != rowCount {
		t.Errorf("%d rows in the table, copied %d", n, rowCount)
	}

	var two int
	if err := conn.QueryRow(ctx, "SELECT 2").Scan(&two); err != nil || two != 2 {
		t.Errorf("a plain query after the COPY returned %d, %v", two, err)
	}
}

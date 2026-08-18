//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Pontus reads a client message into a 32 KiB buffer and treats what arrives as
// a whole message. A statement larger than that arrives in pieces, so this asks
// the plainest question there is about it: does a big query still work?
//
// Big is not exotic. A bulk INSERT, a generated IN list, a long string literal —
// ORMs emit statements past 32 KiB routinely.
func TestQueryLargerThanTheReadBufferWorks(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	for _, size := range []int{1 << 10, 30 << 10, 64 << 10, 256 << 10, 4 << 20} {
		t.Run(fmt.Sprintf("%dKiB", size>>10), func(t *testing.T) {
			// A literal of the requested size, echoed back. Its length is what
			// is checked, so a truncated or spliced statement cannot pass.
			literal := strings.Repeat("x", size)

			var got string
			err := conn.QueryRow(ctx, "SELECT $1::text", literal).Scan(&got)
			if err != nil {
				t.Fatalf("a %d-byte parameter failed: %v", size, err)
			}
			if len(got) != size {
				t.Errorf("got %d bytes back, sent %d", len(got), size)
			}
		})
	}
}

// The same through the simple query protocol, where the whole statement text —
// not a bound parameter — has to cross the buffer boundary.
func TestLargeSimpleQueryWorks(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	for _, size := range []int{30 << 10, 64 << 10, 256 << 10, 4 << 20} {
		t.Run(fmt.Sprintf("%dKiB", size>>10), func(t *testing.T) {
			literal := strings.Repeat("y", size)
			sql := "SELECT '" + literal + "'::text"

			var got string
			// Exec through the simple protocol: pgx sends this as one 'Q'.
			rows, err := conn.Query(ctx, sql)
			if err != nil {
				t.Fatalf("a %d-byte statement failed: %v", len(sql), err)
			}
			defer rows.Close()

			if !rows.Next() {
				t.Fatalf("no row came back from a %d-byte statement: %v", len(sql), rows.Err())
			}
			if err := rows.Scan(&got); err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			if len(got) != size {
				t.Errorf("got %d bytes back, sent %d", len(got), size)
			}
		})
	}
}

var _ = time.Second

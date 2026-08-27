//go:build e2e

package e2e

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Connecting is the one thing every client does, and it is the one thing no
// test did concurrently.
//
// A StartupMessage has no type byte — it opens with a four-byte length. Sent
// down a connection that has already completed its startup, the backend reads
// that length's first byte as a message type and answers `invalid frontend
// message type 0`, killing the connection.
//
// The pool has no notion of a "fresh" connection, so a handshake could draw one
// that a finished session had released. Sequentially it never did, which is why
// every existing test passed. It takes two clients arriving at once — and then
// roughly a quarter of them failed.
func TestConcurrentConnectsDoNotCorruptTheProtocol(t *testing.T) {
	requireBackend(t)

	for name, cfg := range map[string]func(string) string{
		// The default config: passthrough auth, where the client's own packet
		// is what performs the handshake.
		"roomy pool": nil,
		// A pool small enough that connections are constantly recycled, which
		// is what makes a used connection likely to be drawn for a handshake.
		"tiny pool": func(c string) string {
			c = setYAMLScalar(c, "max_conns", "2")
			return setYAMLScalar(c, "min_idle", "0")
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := startStackWith(t, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			const clients = 32
			var wg sync.WaitGroup
			errs := make(chan string, clients*2)

			for i := range clients {
				wg.Go(func() {
					conn, err := connectSimpleOrErr(ctx, s)
					if err != nil {
						errs <- err.Error()
						return
					}
					defer conn.Close(context.Background())

					var got int
					if err := conn.QueryRow(ctx, "SELECT $1::int", i).Scan(&got); err != nil {
						errs <- err.Error()
						return
					}
					if got != i {
						errs <- "client was served another's row"
					}
				})
			}
			wg.Wait()
			close(errs)

			corrupted, other := 0, 0
			var sample, otherSample string
			for e := range errs {
				if strings.Contains(e, "invalid frontend message type") {
					corrupted++
					sample = e
				} else {
					other++
					otherSample = e
				}
			}

			if corrupted > 0 {
				t.Errorf("%d/%d concurrent connects corrupted the wire protocol: %s",
					corrupted, clients, sample)
			}
			if other > 0 {
				t.Errorf("%d/%d concurrent connects failed: %s", other, clients, otherSample)
			}
		})
	}
}

// Sequential connects were always fine, and must stay that way — the guard
// added for the concurrent case must not start refusing connections a single
// client can legitimately use.
func TestSequentialConnectsStillWork(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for i := range 15 {
		conn, err := connectSimpleOrErr(ctx, s)
		if err != nil {
			t.Fatalf("sequential connect %d failed: %v", i, err)
		}
		var n int
		if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
			t.Fatalf("sequential query %d failed: %v", i, err)
		}
		if n != 1 {
			t.Fatalf("sequential query %d returned %d", i, n)
		}
		_ = conn.Close(context.Background())
	}
}

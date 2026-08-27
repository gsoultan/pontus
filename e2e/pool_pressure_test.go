//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// max_conns is the per-identity ceiling. Under pressure a pooler has exactly
// two honest options — make the caller wait for a slot, or refuse it — and one
// dishonest one, which is to hang until something else times out. What a client
// sees when the pool is full is the whole product.
func TestPoolExhaustionIsBoundedAndDiagnosable(t *testing.T) {
	requireBackend(t)

	s := startStackWith(t, func(cfg string) string {
		cfg = setYAMLScalar(cfg, "max_conns", "2")
		cfg = setYAMLScalar(cfg, "min_idle", "0")
		// Long enough that the pool, not this, is what bounds the wait.
		return setYAMLScalar(cfg, "query_timeout", "120s")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Two clients occupy the whole pool with slow statements.
	const holders = 2
	var occupied sync.WaitGroup
	occupied.Add(holders)
	released := make(chan struct{})

	for range holders {
		go func() {
			conn := connectSimple(t, ctx, s)
			defer conn.Close(context.Background())
			occupied.Done()
			// Hold a backend connection for a while.
			_, _ = conn.Exec(ctx, "SELECT pg_sleep(8)")
			<-released
		}()
	}
	occupied.Wait()
	time.Sleep(2 * time.Second) // let the sleeps actually start

	// A third client wants in. It must either get through once a slot frees, or
	// be told the pool is full — within a bounded time, with a real message.
	start := time.Now()
	third := connectSimple(t, ctx, s)
	defer third.Close(context.Background())

	var one int
	err := third.QueryRow(ctx, "SELECT 1").Scan(&one)
	elapsed := time.Since(start)
	close(released)

	if elapsed > 60*time.Second {
		t.Fatalf("a third client waited %v on a two-connection pool; nothing bounded it",
			elapsed.Round(time.Second))
	}

	switch {
	case err == nil:
		t.Logf("waited %v and got through", elapsed.Round(time.Millisecond))
		if one != 1 {
			t.Errorf("SELECT 1 returned %d", one)
		}
	default:
		// Refusal is a legitimate answer. It has to say why.
		low := strings.ToLower(err.Error())
		if !strings.Contains(low, "pool") && !strings.Contains(low, "exhaust") &&
			!strings.Contains(low, "capacity") && !strings.Contains(low, "timeout") {
			t.Errorf("refused after %v with %q, which does not tell an operator "+
				"the pool was full", elapsed.Round(time.Millisecond), err)
		} else {
			t.Logf("refused after %v: %v", elapsed.Round(time.Millisecond), err)
		}
	}
}

// The pool has to come back. A ceiling that is reached once and never released
// is an outage on the database rather than a limit on Pontus.
func TestPoolRecoversAfterPressure(t *testing.T) {
	requireBackend(t)

	s := startStackWith(t, func(cfg string) string {
		cfg = setYAMLScalar(cfg, "max_conns", "3")
		return setYAMLScalar(cfg, "min_idle", "0")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Push well past the ceiling, concurrently.
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for range 20 {
		wg.Go(func() {
			conn := connectSimple(t, ctx, s)
			defer conn.Close(context.Background())
			var n int
			if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)

	failed := 0
	var sample error
	for err := range errs {
		failed++
		if sample == nil {
			sample = err
		}
	}
	if failed > 0 {
		t.Logf("%d/20 refused under pressure (a bounded pool may refuse): %v", failed, sample)
	}

	// Whatever happened above, the pool must serve again afterwards.
	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())
	for i := range 5 {
		var n int
		if err := conn.QueryRow(ctx, "SELECT $1::int", i).Scan(&n); err != nil {
			t.Fatalf("the pool did not recover after pressure (query %d): %v", i, err)
		}
		if n != i {
			t.Fatalf("query %d returned %d", i, n)
		}
	}
}

// A client that gives up while waiting must give its place back. If a cancelled
// acquisition still consumes a slot, the pool leaks one connection per
// impatient client and drains under exactly the load it exists to survive.
func TestAbandonedWaitersDoNotLeakSlots(t *testing.T) {
	requireBackend(t)

	s := startStackWith(t, func(cfg string) string {
		cfg = setYAMLScalar(cfg, "max_conns", "2")
		cfg = setYAMLScalar(cfg, "min_idle", "0")
		return setYAMLScalar(cfg, "query_timeout", "120s")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Fill the pool.
	holder := connectSimple(t, ctx, s)
	defer holder.Close(context.Background())
	holder2 := connectSimple(t, ctx, s)
	defer holder2.Close(context.Background())

	slow := make(chan struct{})
	go func() {
		defer close(slow)
		_, _ = holder.Exec(ctx, "SELECT pg_sleep(6)")
	}()
	go func() {
		_, _ = holder2.Exec(ctx, "SELECT pg_sleep(6)")
	}()
	time.Sleep(1500 * time.Millisecond)

	// A dozen clients ask, then give up almost immediately.
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			short, shortCancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer shortCancel()
			conn, err := connectSimpleOrErr(short, s)
			if err != nil {
				return
			}
			var n int
			_ = conn.QueryRow(short, "SELECT 1").Scan(&n)
			_ = conn.Close(context.Background())
		})
	}
	wg.Wait()
	<-slow

	// Once the holders finish, the pool must be usable again. If abandoned
	// waiters kept their slots, this is where it shows.
	serveCtx, serveCancel := context.WithTimeout(ctx, 45*time.Second)
	defer serveCancel()

	after := connectSimple(t, serveCtx, s)
	defer after.Close(context.Background())
	var n int
	if err := after.QueryRow(serveCtx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("the pool never recovered from clients that gave up waiting: %v", err)
	}
	if n != 1 {
		t.Errorf("SELECT 1 returned %d", n)
	}
}

// connectSimpleOrErr is connectSimple for callers that expect to fail — a
// client giving up while the pool is full is the case under test, not a fault.
func connectSimpleOrErr(ctx context.Context, s *stack) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), s.proxyAddr, backendDB()))
	if err != nil {
		return nil, err
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return pgx.ConnectConfig(ctx, cfg)
}

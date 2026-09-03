//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"
	"time"
)

// Every deploy and every restart runs this path, and nothing exercised it.
//
// A pooler that drops in-flight statements on a restart turns a routine deploy
// into a burst of errors in the application's database layer, which is the
// worst place for them to surface. One that waits forever turns it into a hung
// deploy that a supervisor eventually SIGKILLs — the same dropped statements,
// later and louder.
//
// Both halves are asserted: the statement must survive, and the process must
// still exit promptly.
func TestGracefulShutdownWithAQueryInFlight(t *testing.T) {
	requireBackend(t)

	s := startStackWith(t, func(cfg string) string {
		return setYAMLScalar(cfg, "query_timeout", "120s")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("baseline query failed: %v", err)
	}

	// A statement that outlives the shutdown signal.
	type outcome struct {
		err     error
		elapsed time.Duration
	}
	queryDone := make(chan outcome, 1)
	go func() {
		start := time.Now()
		_, err := conn.Exec(ctx, "SELECT pg_sleep(8)")
		queryDone <- outcome{err, time.Since(start)}
	}()
	time.Sleep(2 * time.Second)

	if s.cmd == nil || s.cmd.Process == nil {
		t.Fatal("no proxy process to signal")
	}
	start := time.Now()
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("could not signal the proxy: %v", err)
	}

	exited := make(chan struct{})
	go func() {
		_, _ = s.cmd.Process.Wait()
		close(exited)
	}()

	// The statement must finish rather than be cut off. pg_sleep(8) started two
	// seconds before the signal, so a drain means about six more seconds.
	select {
	case got := <-queryDone:
		if got.err != nil {
			t.Errorf("the statement in flight was dropped by the shutdown after %v: %v",
				got.elapsed.Round(time.Millisecond), got.err)
		} else {
			t.Logf("the statement in flight completed, %v after it started",
				got.elapsed.Round(time.Millisecond))
		}
	case <-time.After(60 * time.Second):
		t.Error("the statement never returned")
	}

	select {
	case <-exited:
		shutdown := time.Since(start)
		t.Logf("proxy exited %v after SIGINT", shutdown.Round(time.Millisecond))
		// Draining is not an excuse to hang: a supervisor that has to SIGKILL
		// drops whatever was in flight anyway.
		if shutdown > 60*time.Second {
			t.Errorf("shutdown took %v; a supervisor would have killed it",
				shutdown.Round(time.Second))
		}
	case <-time.After(90 * time.Second):
		_ = s.cmd.Process.Kill()
		t.Fatal("the proxy never exited after SIGINT; a deploy would hang here")
	}
}

// A shutdown with nothing in flight should be prompt. Waiting out a timeout on
// an idle proxy makes every deploy slower than it needs to be.
func TestIdleShutdownIsPrompt(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("baseline query failed: %v", err)
	}
	_ = conn.Close(context.Background())

	// Give the proxy a moment to notice the client went away.
	time.Sleep(1 * time.Second)

	start := time.Now()
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("could not signal the proxy: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_, _ = s.cmd.Process.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		elapsed := time.Since(start)
		t.Logf("idle proxy exited in %v", elapsed.Round(time.Millisecond))
		if elapsed > 30*time.Second {
			t.Errorf("an idle proxy took %v to shut down", elapsed.Round(time.Second))
		}
	case <-time.After(60 * time.Second):
		_ = s.cmd.Process.Kill()
		t.Fatal("an idle proxy never exited after SIGINT")
	}
}

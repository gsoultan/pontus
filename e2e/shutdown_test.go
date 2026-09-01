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
// What it currently does, measured: a statement in flight is dropped, and the
// proxy is gone about five milliseconds after the signal. Its client sees the
// connection close.
//
// That is not what the code intends. Gateway.Stop exists and its comment says
// it "waits for all active connections to close" — but nothing reaches it from
// a signal (PontusService.Stop cancels the context and returns), and it would
// not drain if it did, because it cancels the gateway's own context before
// waiting on the WaitGroup, which aborts the very sessions it means to wait
// for. Draining is written but not wired up and not ordered correctly.
//
// Asserted as it behaves rather than as it should, so the day someone finishes
// the drain the change is visible here. What is asserted unconditionally is
// what a supervisor cares about: the process must exit, and the client must
// learn the outcome instead of waiting on a socket nobody is serving.
func TestGracefulShutdownWithAQueryInFlight(t *testing.T) {
	requireBackend(t)

	s := startStackWith(t, func(cfg string) string {
		return setYAMLScalar(cfg, "query_timeout", "120s")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	// Confirm the session works before anything is signalled.
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("baseline query failed: %v", err)
	}

	// A statement that outlives the shutdown signal.
	queryDone := make(chan error, 1)
	go func() {
		_, err := conn.Exec(ctx, "SELECT pg_sleep(8)")
		queryDone <- err
	}()
	time.Sleep(2 * time.Second)

	// SIGINT, as a supervisor or a Ctrl-C would.
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

	select {
	case <-exited:
		shutdown := time.Since(start)
		t.Logf("proxy exited %v after SIGINT", shutdown.Round(time.Millisecond))
		// It must not hang. A supervisor that has to SIGKILL is a supervisor
		// that drops whatever was in flight.
		if shutdown > 60*time.Second {
			t.Errorf("shutdown took %v; a supervisor would have killed it",
				shutdown.Round(time.Second))
		}
	case <-time.After(90 * time.Second):
		_ = s.cmd.Process.Kill()
		t.Fatal("the proxy never exited after SIGINT; a deploy would hang here")
	}

	// Whatever the shutdown policy, the client must learn the outcome rather
	// than be left waiting on a socket nobody is serving.
	select {
	case err := <-queryDone:
		if err != nil {
			t.Logf("the in-flight statement ended with: %v", err)
		} else {
			t.Log("the in-flight statement completed before shutdown finished")
		}
	case <-time.After(60 * time.Second):
		t.Error("the in-flight statement never returned after the proxy exited; " +
			"its client is waiting on a connection nobody is serving")
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

//go:build e2e

package e2e

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// Cancelling a running query opens a *second* connection and sends a
// CancelRequest carrying the backend PID and secret from the first one's
// BackendKeyData. Nothing about it goes through the session it cancels.
//
// A proxy therefore has to recognise that request and forward it to whichever
// backend is running the statement. If it does not, Ctrl+C in psql, a
// statement timeout in an application, and every "cancel this report" button
// silently does nothing.
func TestRunningQueryCanBeCancelled(t *testing.T) {
	requireBackend(t)

	// query_timeout well above the cancel, so what ends the query is the
	// cancellation and not Pontus's own bound.
	s := startStackWith(t, func(cfg string) string {
		return setYAMLScalar(cfg, "query_timeout", "120s")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	// Make sure the session is established before timing anything.
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("warm-up query failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := conn.Exec(ctx, "SELECT pg_sleep(60)")
		done <- err
	}()

	time.Sleep(2 * time.Second)

	cancelCtx, cancelDone := context.WithTimeout(ctx, 20*time.Second)
	defer cancelDone()
	if err := conn.PgConn().CancelRequest(cancelCtx); err != nil {
		t.Fatalf("sending the cancel request failed: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled pg_sleep(60) completed successfully")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "cancel") {
			t.Errorf("query ended with %q, which does not look like a cancellation", err)
		}
		t.Logf("cancelled: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the cancel request did not stop a 60-second query")
	}
}

// The same under Pontus-side authentication, which is a different path: there
// the BackendKeyData is one Pontus writes to the client itself rather than one
// it relays, so it is remembered somewhere else entirely.
func TestCancelWorksUnderPontusAuth(t *testing.T) {
	requireBackend(t)

	s := authStackOnLoopback(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("warm-up query failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := conn.Exec(ctx, "SELECT pg_sleep(60)")
		done <- err
	}()

	time.Sleep(2 * time.Second)

	cancelCtx, cancelDone := context.WithTimeout(ctx, 20*time.Second)
	defer cancelDone()
	if err := conn.PgConn().CancelRequest(cancelCtx); err != nil {
		t.Fatalf("sending the cancel request failed: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled pg_sleep(60) completed successfully")
		}
		t.Logf("cancelled: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the cancel request did not stop a 60-second query under Pontus auth")
	}
}

// A cancel request naming a process Pontus has never seen must be dropped
// quietly. Answering differently tells an unauthenticated caller which process
// ids exist, and the secret is the backend's to check, never Pontus's.
func TestCancelForAnUnknownProcessIsHarmless(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	// A CancelRequest for a process that does not exist, sent by hand.
	raw := []byte{0, 0, 0, 16, 0x04, 0xd2, 0x16, 0x2e, 0x7f, 0xff, 0xff, 0xff, 0, 0, 0, 1}
	side, err := net.DialTimeout("tcp", s.proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("could not reach the proxy: %v", err)
	}
	_, _ = side.Write(raw)
	_ = side.Close()

	// The proxy has to still be working.
	var two int
	if err := conn.QueryRow(ctx, "SELECT 2").Scan(&two); err != nil || two != 2 {
		t.Errorf("the proxy stopped working after a bogus cancel: %d, %v", two, err)
	}
}

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// LISTEN/NOTIFY delivers messages the client never asked for. They arrive on
// the backend connection whenever the notifying transaction commits, which is
// usually while the listening session is sitting idle — and an idle session is
// exactly when a request/reply proxy is not reading its backend.
//
// Pontus pins a session that LISTENs, so the connection is held. The question
// this asks is whether the notification actually reaches the client.
func TestNotificationReachesAListeningClient(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	listener := connectSimple(t, ctx, s)
	defer listener.Close(context.Background())

	if _, err := listener.Exec(ctx, "LISTEN pontus_probe"); err != nil {
		t.Fatalf("LISTEN failed: %v", err)
	}

	notifier := connectSimple(t, ctx, s)
	defer notifier.Close(context.Background())
	if _, err := notifier.Exec(ctx, "NOTIFY pontus_probe, 'hello'"); err != nil {
		t.Fatalf("NOTIFY failed: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 20*time.Second)
	defer waitCancel()

	n, err := listener.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("no notification arrived: %v", err)
	}
	if n.Channel != "pontus_probe" || n.Payload != "hello" {
		t.Errorf("got %q/%q, want pontus_probe/hello", n.Channel, n.Payload)
	}
}

// The watcher is interrupted every time the client sends a statement, and it is
// reading the same socket the statement's reply will arrive on. If it ever
// dropped a partial message, or forwarded one after the session resumed, the
// stream would be out of step from that point on.
//
// So: notify, query, notify, query — repeatedly.
func TestSessionStaysUsableWhileListening(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	listener := connectSimple(t, ctx, s)
	defer listener.Close(context.Background())
	if _, err := listener.Exec(ctx, "LISTEN pontus_mix"); err != nil {
		t.Fatalf("LISTEN failed: %v", err)
	}

	notifier := connectSimple(t, ctx, s)
	defer notifier.Close(context.Background())

	for i := range 5 {
		payload := fmt.Sprintf("payload-%d", i)
		if _, err := notifier.Exec(ctx, "NOTIFY pontus_mix, '"+payload+"'"); err != nil {
			t.Fatalf("NOTIFY %d failed: %v", i, err)
		}

		waitCtx, waitCancel := context.WithTimeout(ctx, 15*time.Second)
		n, err := listener.WaitForNotification(waitCtx)
		waitCancel()
		if err != nil {
			t.Fatalf("notification %d never arrived: %v", i, err)
		}
		if n.Payload != payload {
			t.Errorf("notification %d carried %q, want %q", i, n.Payload, payload)
		}

		// The listener must still be a working session in between.
		var got int
		if err := listener.QueryRow(ctx, "SELECT $1::int", i).Scan(&got); err != nil {
			t.Fatalf("the listening session broke after notification %d: %v", i, err)
		}
		if got != i {
			t.Fatalf("query after notification %d returned %d", i, got)
		}
	}
}

// A notification delivered while the listener is mid-query must not be mixed
// into that query's reply.
func TestNotificationDuringAQueryDoesNotCorruptIt(t *testing.T) {
	requireBackend(t)

	s := startStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	listener := connectSimple(t, ctx, s)
	defer listener.Close(context.Background())
	if _, err := listener.Exec(ctx, "LISTEN pontus_race"); err != nil {
		t.Fatalf("LISTEN failed: %v", err)
	}

	notifier := connectSimple(t, ctx, s)
	defer notifier.Close(context.Background())

	// Notify steadily while the listener runs queries — steadily, not as fast
	// as the machine allows. The point is to land notifications in the gaps
	// between statements, and an unthrottled loop on a background context is a
	// load generator that makes every other test in the suite slower and can
	// outlive the test that started it.
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
				nctx, ncancel := context.WithTimeout(ctx, 5*time.Second)
				_, _ = notifier.Exec(nctx, "NOTIFY pontus_race, 'x'")
				ncancel()
			}
		}
	}()
	defer func() {
		close(stop)
		<-stopped
	}()

	for i := range 200 {
		var got int
		if err := listener.QueryRow(ctx, "SELECT $1::int", i).Scan(&got); err != nil {
			t.Fatalf("query %d failed while notifications were arriving: %v", i, err)
		}
		if got != i {
			t.Fatalf("query %d returned %d — the reply was mixed with a notification", i, got)
		}
	}
}

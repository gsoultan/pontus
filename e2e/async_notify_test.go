//go:build e2e

package e2e

import (
	"context"
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

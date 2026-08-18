package protocol

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func readyForQuery() []byte { return []byte{'Z', 0, 0, 0, 5, 'I'} }

// A backend that refuses a replayed statement used to be indistinguishable from
// one that accepted it: the loop scanned for ReadyForQuery and stepped straight
// past the rejection, so the session ran on a connection it was never
// configured on.
func TestAwaitReplyReportsARefusal(t *testing.T) {
	client, backend := net.Pipe()
	defer client.Close()
	defer backend.Close()

	go func() {
		_, _ = backend.Write(ErrorResponse(SeverityError, "42501",
			`permission denied to set parameter "search_path"`))
		_, _ = backend.Write(readyForQuery())
	}()

	err := awaitReply(t.Context(), client, make([]byte, 4096), "replaying SET search_path")
	if err == nil {
		t.Fatal("a refused statement was reported as a successful replay")
	}
	// The reason has to survive to the log, or the operator sees only that
	// "replay failed" on a connection that looked healthy.
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error does not carry the backend's reason: %v", err)
	}
	if !strings.Contains(err.Error(), "search_path") {
		t.Errorf("error does not name the statement that failed: %v", err)
	}
}

// The context was accepted and never used, so a backend that took the statement
// and then went quiet held the session forever with no deadline anywhere.
func TestAwaitReplyHonoursTheDeadline(t *testing.T) {
	client, backend := net.Pipe()
	defer client.Close()
	defer backend.Close()
	// backend never writes.

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := awaitReply(ctx, client, make([]byte, 4096), "replaying SET x")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a silent backend was reported as a successful replay")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("error is %v, want a deadline", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("waited %v for a 200ms deadline", elapsed)
	}
}

func TestAwaitReplyAcceptsASuccess(t *testing.T) {
	client, backend := net.Pipe()
	defer client.Close()
	defer backend.Close()

	go func() {
		_, _ = backend.Write([]byte{'C', 0, 0, 0, 8, 'S', 'E', 'T', 0})
		_, _ = backend.Write(readyForQuery())
	}()

	if err := awaitReply(t.Context(), client, make([]byte, 4096), "replaying SET x"); err != nil {
		t.Fatalf("a successful replay was reported as a failure: %v", err)
	}
}

// A reply arriving in fragments must still be framed correctly — the loop this
// replaces restarted its scan at the top of every read, so a message split
// across two reads was misparsed.
func TestAwaitReplyHandlesASplitReply(t *testing.T) {
	client, backend := net.Pipe()
	defer client.Close()
	defer backend.Close()

	go func() {
		full := append(ErrorResponse(SeverityError, "42501", "denied"), readyForQuery()...)
		for _, b := range full {
			_, _ = backend.Write([]byte{b})
		}
	}()

	err := awaitReply(t.Context(), client, make([]byte, 4096), "replaying SET x")
	if err == nil {
		t.Fatal("a refusal split across reads was reported as success")
	}
}

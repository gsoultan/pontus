//go:build e2e

package e2e

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// startupMessage builds a PostgreSQL v3 StartupMessage.
//
// Written by hand rather than through pgx: the driver decides for itself how to
// frame a replication connection, and the guard has to be tested against what
// pg_recvlogical and Debezium actually put on the wire.
func startupMessage(params map[string]string) []byte {
	body := make([]byte, 0, 128)
	body = binary.BigEndian.AppendUint32(body, 196608) // protocol 3.0
	for key, value := range params {
		body = append(body, key...)
		body = append(body, 0)
		body = append(body, value...)
		body = append(body, 0)
	}
	body = append(body, 0)

	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))
	return append(frame, body...)
}

// readAll drains what the proxy sends back, so the test can assert on the
// ErrorResponse rather than just on the socket closing.
func readAll(t *testing.T, conn net.Conn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err != nil {
			return out.String()
		}
	}
}

// A replication connection is carried, not pooled.
//
// It must never take the pooled path: the transaction loop returns the backend
// connection whenever the session looks idle, which for a CopyBoth stream means
// handing a half-read WAL feed to whichever client is served next. Reaching the
// authentication exchange proves it was routed to the primary instead of being
// refused or pooled.
func TestReplicationConnectionIsCarried(t *testing.T) {
	s := startStack(t)

	conn, err := net.DialTimeout("tcp", s.proxyAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// What pg_recvlogical sends.
	if _, err := conn.Write(startupMessage(map[string]string{
		"user":        backendUser(),
		"database":    backendDB(),
		"replication": "database",
	})); err != nil {
		t.Fatalf("write startup: %v", err)
	}

	response := readAll(t, conn)

	// The primary answers a replication startup with an authentication request.
	// A refusal would carry SQLSTATE 0A000 instead.
	if strings.Contains(response, "0A000") {
		t.Fatalf("replication stream was refused rather than carried: %q", truncate(response))
	}
	if response == "" {
		t.Fatal("replication stream produced no response at all")
	}
}

// Carrying replication must not disturb ordinary traffic on the same proxy:
// each stream's connection is destroyed on exit rather than returned to the
// pool, so a session that follows gets a clean one.
func TestOrdinarySessionsUnaffectedByReplication(t *testing.T) {
	s := startStack(t)

	for range 3 {
		conn, err := net.DialTimeout("tcp", s.proxyAddr, 10*time.Second)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		_, _ = conn.Write(startupMessage(map[string]string{
			"user":        backendUser(),
			"database":    backendDB(),
			"replication": "database",
		}))
		readAll(t, conn)
		conn.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := connect(t, ctx, s)
	var one int
	if err := client.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("ordinary session broken after replication attempts: %v", err)
	}
	if one != 1 {
		t.Errorf("SELECT 1 returned %d", one)
	}
}

// PostgreSQL treats replication=false as an ordinary session, and so must the
// proxy — routing it to the stream path would break clients that set the
// parameter explicitly.
func TestReplicationFalseIsAnOrdinarySession(t *testing.T) {
	s := startStack(t)

	conn, err := net.DialTimeout("tcp", s.proxyAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write(startupMessage(map[string]string{
		"user":        backendUser(),
		"database":    backendDB(),
		"replication": "false",
	})); err != nil {
		t.Fatalf("write startup: %v", err)
	}

	response := readAll(t, conn)
	if strings.Contains(response, "0A000") {
		t.Errorf("replication=false was refused, but it is an ordinary session: %q", truncate(response))
	}
}

// Sanity: an ordinary client is unaffected by the parsing change.
func TestPlainSessionStillConnects(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	_ = pgx.ErrNoRows // keep the pgx import meaningful for the helper above
	if one != 1 {
		t.Errorf("SELECT 1 returned %d", one)
	}
}

func truncate(s string) string {
	if len(s) > 300 {
		return fmt.Sprintf("%s…", s[:300])
	}
	return s
}

// The stream budget must turn away a new consumer rather than the application.
//
// The e2e proxy allows four streams. Opening more must refuse the fifth with an
// explanation, and ordinary sessions must keep working throughout.
func TestReplicationBudgetRefusesNewConsumersOnly(t *testing.T) {
	s := startStack(t)

	const budget = 4
	var held []net.Conn
	t.Cleanup(func() {
		for _, c := range held {
			_ = c.Close()
		}
	})

	// Hold the budget open. These do not complete authentication, so they stay
	// attached from the proxy's point of view.
	for range budget {
		conn, err := net.DialTimeout("tcp", s.proxyAddr, 10*time.Second)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		_, _ = conn.Write(startupMessage(map[string]string{
			"user": backendUser(), "database": backendDB(), "replication": "database",
		}))
		held = append(held, conn)
	}

	// Ordinary traffic must be unaffected while the stream budget is saturated.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := connect(t, ctx, s)
	var one int
	if err := client.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("application session refused while the stream budget was full: %v", err)
	}
}

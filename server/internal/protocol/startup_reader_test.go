package protocol

import (
	"errors"
	"net"
	"testing"
	"time"
)

// startupBytes builds a PostgreSQL StartupMessage for the given parameters.
func startupBytes(params map[string]string) []byte {
	body := []byte{0, 3, 0, 0} // protocol 3.0
	for k, v := range params {
		body = append(body, k...)
		body = append(body, 0)
		body = append(body, v...)
		body = append(body, 0)
	}
	body = append(body, 0)

	total := len(body) + 4
	out := []byte{byte(total >> 24), byte(total >> 16), byte(total >> 8), byte(total)}
	return append(out, body...)
}

// clientPipe returns a connection carrying the given bytes from the "client".
func clientPipe(t *testing.T, payload []byte) net.Conn {
	t.Helper()
	ours, theirs := net.Pipe()
	go func() {
		_, _ = theirs.Write(payload)
	}()
	t.Cleanup(func() { ours.Close(); theirs.Close() })
	_ = ours.SetDeadline(time.Now().Add(5 * time.Second))
	return ours
}

// The identity has to be known before a backend is chosen — that is the whole
// point of reading the startup packet first.
func TestReadStartupYieldsIdentityWithoutABackend(t *testing.T) {
	p := &PostgresHandler{}
	state := &SessionState{}

	req, err := p.ReadStartup(clientPipe(t, startupBytes(map[string]string{
		"user": "alice", "database": "sales",
	})), state)
	if err != nil {
		t.Fatalf("ReadStartup: %v", err)
	}

	if req.User != "alice" || req.Database != "sales" {
		t.Errorf("read %q/%q, want alice/sales", req.User, req.Database)
	}
	if state.User != "alice" || state.Database != "sales" {
		t.Errorf("session state %q/%q, want alice/sales", state.User, state.Database)
	}
	if len(req.Raw) == 0 {
		t.Error("the raw packet was not kept; it has to be forwarded to the backend")
	}
}

// PostgreSQL defaults the database to the user name.
func TestReadStartupDefaultsDatabaseToUser(t *testing.T) {
	p := &PostgresHandler{}
	state := &SessionState{}

	req, err := p.ReadStartup(clientPipe(t, startupBytes(map[string]string{"user": "carol"})), state)
	if err != nil {
		t.Fatalf("ReadStartup: %v", err)
	}
	if req.Database != "carol" {
		t.Errorf("database = %q, want carol", req.Database)
	}
}

// A replication client must be recognised before a pooled connection is taken.
// It used to acquire one, discover the request, and hand it straight back.
func TestReadStartupReportsReplicationBeforeAnyBackendIsChosen(t *testing.T) {
	p := &PostgresHandler{}
	state := &SessionState{}

	_, err := p.ReadStartup(clientPipe(t, startupBytes(map[string]string{
		"user": "repl", "database": "postgres", "replication": "database",
	})), state)

	if !errors.Is(err, ErrReplicationRequested) {
		t.Fatalf("err = %v, want ErrReplicationRequested", err)
	}
	if len(state.StartupPacket) == 0 {
		t.Error("the startup packet was not preserved for the replication path")
	}
}

// CompleteHandshake without a request is a programming error, not a panic.
func TestCompleteHandshakeRejectsANilRequest(t *testing.T) {
	p := &PostgresHandler{}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	if err := p.CompleteHandshake(t.Context(), client, server, nil, &SessionState{}); err == nil {
		t.Error("a nil startup request was accepted")
	}
}

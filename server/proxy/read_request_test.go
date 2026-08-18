package proxy

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// simpleQuery builds a well-formed 'Q' message of the requested payload size.
func simpleQuery(sql string) []byte {
	length := 4 + len(sql) + 1
	out := []byte{'Q', byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	out = append(out, sql...)
	return append(out, 0)
}

func framingGateway(t *testing.T, maxMessage int) *Gateway {
	t.Helper()
	g := NewGateway(protocol.NewPostgresHandler(), &mockBalancer{}, nil,
		&config.Options{MaxMessageBytes: maxMessage}, nil)
	t.Cleanup(g.cancel)
	return g
}

// A statement larger than the read buffer used to reach the backend as a
// fragment. The backend waited for the rest, sent nothing, and the session hung
// until query_timeout killed it — so every statement over 32 KiB failed.
func TestReadRequestAssemblesAMessageAcrossReads(t *testing.T) {
	g := framingGateway(t, 0)

	msg := simpleQuery("SELECT '" + strings.Repeat("x", 200<<10) + "'")

	client, peer := net.Pipe()
	defer client.Close()
	go func() {
		defer peer.Close()
		// Deliberately dribbled, so nothing can pass by arriving whole.
		for i := 0; i < len(msg); i += 4096 {
			_, _ = peer.Write(msg[i:min(i+4096, len(msg))])
		}
	}()

	got, err := g.readRequest(client, make([]byte, 32*1024))
	if err != nil {
		t.Fatalf("assembling a %d-byte message failed: %v", len(msg), err)
	}
	if len(got) != len(msg) {
		t.Fatalf("assembled %d bytes, sent %d", len(got), len(msg))
	}
	if string(got) != string(msg) {
		t.Error("assembled bytes differ from what was sent")
	}
}

// The common case must not pay for the uncommon one: one whole message in one
// read returns the read buffer itself.
func TestReadRequestDoesNotCopyTheCommonCase(t *testing.T) {
	g := framingGateway(t, 0)
	msg := simpleQuery("SELECT 1")

	client, peer := net.Pipe()
	defer client.Close()
	go func() { defer peer.Close(); _, _ = peer.Write(msg) }()

	buf := make([]byte, 32*1024)
	got, err := g.readRequest(client, buf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(got) != len(msg) {
		t.Fatalf("read %d bytes, sent %d", len(got), len(msg))
	}
	if &got[0] != &buf[0] {
		t.Error("a single whole message was copied out of the read buffer")
	}
}

// The length driving the assembly is a number the client chose, so it has to be
// bounded or a client can make Pontus reserve memory on request.
func TestReadRequestRefusesAnOversizedMessage(t *testing.T) {
	g := framingGateway(t, 64*1024)

	// Claim 512 MiB and send only a header.
	header := []byte{'Q', 0x20, 0, 0, 0, 'S', 'E', 'L'}

	client, peer := net.Pipe()
	defer client.Close()
	go func() { defer peer.Close(); _, _ = peer.Write(header) }()

	if _, err := g.readRequest(client, make([]byte, 32*1024)); err == nil {
		t.Fatal("a message claiming 512 MiB was accepted against a 64 KiB limit")
	} else if !strings.Contains(err.Error(), "max_message_bytes") {
		t.Errorf("error does not name the limit that refused it: %v", err)
	}
}

// A header split across reads is a request for more bytes, not an error.
func TestReadRequestHandlesASplitHeader(t *testing.T) {
	g := framingGateway(t, 0)
	msg := simpleQuery("SELECT 1")

	client, peer := net.Pipe()
	defer client.Close()
	go func() {
		defer peer.Close()
		_, _ = peer.Write(msg[:2]) // less than the five-byte header
		_, _ = peer.Write(msg[2:])
	}()

	got, err := g.readRequest(client, make([]byte, 32*1024))
	if err != nil && err != io.EOF {
		t.Fatalf("a split header failed: %v", err)
	}
	if len(got) != len(msg) {
		t.Errorf("assembled %d bytes, sent %d", len(got), len(msg))
	}
}

// A read can end part-way through a *later* message when a client pipelines.
// Assembling only the first would forward a fragment of the second and leave
// the backend waiting for the rest of it — the same hang, one message along.
func TestReadRequestCompletesALaterMessage(t *testing.T) {
	g := framingGateway(t, 0)

	small := simpleQuery("SELECT 1")
	big := simpleQuery("SELECT '" + strings.Repeat("z", 100<<10) + "'")
	stream := append(append([]byte{}, small...), big...)

	client, peer := net.Pipe()
	defer client.Close()
	go func() {
		defer peer.Close()
		for i := 0; i < len(stream); i += 8192 {
			_, _ = peer.Write(stream[i:min(i+8192, len(stream))])
		}
	}()

	got, err := g.readRequest(client, make([]byte, 32*1024))
	if err != nil {
		t.Fatalf("assembling a pipelined pair failed: %v", err)
	}
	if len(got) < len(stream) {
		t.Fatalf("returned %d bytes with a message left unfinished; sent %d",
			len(got), len(stream))
	}
	if trailingNeed(protocol.NewPostgresHandler(), got) != 0 {
		t.Error("the returned buffer still ends part-way through a message")
	}
}

// Nothing here may loop forever on a client that stops mid-message.
func TestReadRequestStopsWhenTheClientGoesAway(t *testing.T) {
	g := framingGateway(t, 0)
	msg := simpleQuery("SELECT '" + strings.Repeat("q", 100<<10) + "'")

	client, peer := net.Pipe()
	defer client.Close()
	go func() {
		_, _ = peer.Write(msg[:40<<10]) // less than promised, then hang up
		peer.Close()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := g.readRequest(client, make([]byte, 32*1024)); err == nil {
			t.Error("a truncated message was reported as complete")
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("readRequest did not return after the client disconnected mid-message")
	}
}

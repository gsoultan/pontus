package protocol

import (
	"bytes"
	"testing"
)

// One read is not one message. A client may pipeline several into a single
// segment, and reading to the end of the buffer appended whatever followed to
// the statement being examined.
func TestExtractQueryHonoursTheLengthPrefix(t *testing.T) {
	h := NewPostgresHandler()
	pipelined := append(query("SELECT 1"), query("DROP TABLE users")...)

	got := h.extractQueryBytes(pipelined)
	if want := "SELECT 1"; string(got) != want {
		t.Errorf("extracted %q from a pipelined buffer, want %q", got, want)
	}
	if bytes.Contains(got, []byte("DROP")) {
		t.Error("the following message bled into the statement being examined")
	}
}

// The terminator is framing, not content. Leaving it on put a NUL into the
// normalized text used for metrics and into every tracked SET value.
func TestExtractQueryDropsTheTerminator(t *testing.T) {
	h := NewPostgresHandler()

	if got := h.extractQueryBytes(query("SELECT 1")); bytes.IndexByte(got, 0) >= 0 {
		t.Errorf("extracted %q, which still carries a NUL", got)
	}
	if got := h.NormalizeQuery(query("SELECT 1")); bytes.IndexByte([]byte(got), 0) >= 0 {
		t.Errorf("normalized to %q, which still carries a NUL", got)
	}
}

// The prefix is client-controlled, so it is bounded against what arrived.
// Nothing here may panic or read past the buffer.
func TestExtractQueryRejectsAnImpossibleLength(t *testing.T) {
	h := NewPostgresHandler()

	for name, msg := range map[string][]byte{
		"length larger than the buffer": {'Q', 0x7f, 0xff, 0xff, 0xff, 'S', 'E', 'L', 0},
		"length below the header":       {'Q', 0, 0, 0, 1, 'S', 'E', 'L', 0},
		"zero length":                   {'Q', 0, 0, 0, 0, 'S', 'E', 'L', 0},
		"truncated message":             {'Q', 0, 0, 0, 32, 'S', 'E', 'L', 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := h.extractQueryBytes(msg); got != nil {
				t.Errorf("extracted %q from a malformed message", got)
			}
			// A statement that cannot be read must not be classified read-only:
			// an unreadable query routed to a replica is a write on a replica.
			if info := h.ClassifyQuery(msg); info.ReadOnly {
				t.Error("a malformed message classified as read-only")
			}
		})
	}
}

// A tracked SET is replayed onto the next connection verbatim, so its value has
// to be exactly what the client set — not the rest of the buffer.
func TestTrackedSetValueIsClean(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	pipelined := append(query("SET application_name = 'reporting'"), query("SELECT 1")...)
	h.TrackSessionState(state, pipelined)

	if got, want := state.Vars["application_name"], "= 'reporting'"; got != want {
		t.Errorf("tracked %q, want %q", got, want)
	}
}

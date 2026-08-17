package protocol

import "testing"

// A comment before the verb must not hide it. The classifier decides read
// versus write, and a write mistaken for a read is routed to a replica.
func TestCommentsDoNotHideTheVerb(t *testing.T) {
	h := NewPostgresHandler()
	for _, sql := range []string{
		"/*hint*/ UPDATE t SET x = 1",
		"-- lead\nUPDATE t SET x = 1",
		"/**/DELETE FROM t",
		"UPDATE/**/t SET x = 1",
		"/* multi\nline */ INSERT INTO t VALUES (1)",
	} {
		if info := h.ClassifyQuery(query(sql)); info.ReadOnly {
			t.Errorf("classified as read-only, would be routed to a replica: %q", sql)
		}
	}
}

// Query-annotation tools (sqlcommenter and friends) prepend a comment to every
// statement an ORM emits. If that hides the verb from pin detection, a LISTEN
// goes unnoticed and its connection is returned to the pool — the client stops
// receiving notifications with no error anywhere.
func TestCommentsDoNotHidePinning(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  PinReason
	}{
		{"/*app:reports*/ LISTEN events", PinListen},
		{"-- traceparent: 00-abc\nLISTEN events", PinListen},
		{"/*x*/ CREATE TEMP TABLE staging (id int)", PinTempTable},
		{"/*x*/ SELECT temp FROM sensors", 0},
	} {
		if got := pinReasonFor([]byte(tc.query)); got != tc.want {
			t.Errorf("pinReasonFor(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

// The same for the statements that lift a pin: a commented DISCARD ALL that
// goes unrecognised holds its connection for the rest of the session.
func TestCommentsDoNotHideUnpinning(t *testing.T) {
	if got := unpinReasonFor([]byte("/*app:reports*/ DISCARD ALL")); got != ^PinReason(0) {
		t.Errorf("a commented DISCARD ALL cleared %v, want everything", got)
	}
}

// PostgreSQL block comments nest, unlike C's. Stopping at the first `*/` would
// leave the tail to be read as statement text.
func TestNestedBlockCommentsAreOneComment(t *testing.T) {
	if got := pinReasonFor([]byte("/* a /* b */ c */ LISTEN events")); got != PinListen {
		t.Errorf("nested comment hid the verb: got %v", got)
	}
	// An unterminated comment swallows the rest, which must not pin anything
	// and must not loop.
	if got := pinReasonFor([]byte("/* unterminated LISTEN events")); got != 0 {
		t.Errorf("an unterminated comment yielded %v", got)
	}
}

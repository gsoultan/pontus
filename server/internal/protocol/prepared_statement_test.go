package protocol

import "testing"

func parseMsg(name, sql string) []byte {
	body := append([]byte(name), 0)
	body = append(body, sql...)
	body = append(body, 0, 0, 0) // NUL, then int16 parameter count of zero
	length := 4 + len(body)
	out := []byte{'P', byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	return append(out, body...)
}

func closeMsg(name string) []byte {
	body := append([]byte{'S'}, name...)
	body = append(body, 0)
	length := 4 + len(body)
	out := []byte{'C', byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	return append(out, body...)
}

func TestPreparedStatementsAreTracked(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	h.TrackPreparedStatement(state, parseMsg("s1", "SELECT 1"))
	if got := state.Stmts["s1"]; got != "SELECT 1" {
		t.Fatalf("tracked %q, want %q", got, "SELECT 1")
	}
}

// Nothing removed entries, so a session that prepares and closes in a loop —
// what a driver with a bounded statement cache does — grew the map for the life
// of the connection and replayed statements the backend had been told to drop.
func TestClosedStatementsAreForgotten(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	h.TrackPreparedStatement(state, parseMsg("s1", "SELECT 1"))
	h.ForgetPreparedStatement(state, closeMsg("s1"))

	if _, still := state.Stmts["s1"]; still {
		t.Error("a closed statement is still tracked and would be replayed")
	}
}

// Closing a *portal* is not closing a statement.
func TestClosingAPortalKeepsTheStatement(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}
	h.TrackPreparedStatement(state, parseMsg("s1", "SELECT 1"))

	portal := []byte{'C', 0, 0, 0, 8, 'P', 's', '1', 0}
	h.ForgetPreparedStatement(state, portal)

	if _, ok := state.Stmts["s1"]; !ok {
		t.Error("closing a portal dropped the prepared statement")
	}
}

// The map is keyed by a client-supplied name and replayed on every backend
// switch, so it needs a cap — and a session past it must stop being movable
// rather than be replayed missing statements it prepared.
func TestPreparedStatementsAreBounded(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	for i := range maxSessionStmts * 2 {
		h.TrackPreparedStatement(state, parseMsg("s"+itoa(i), "SELECT "+itoa(i)))
	}

	if len(state.Stmts) > maxSessionStmts {
		t.Errorf("tracked %d statements, cap is %d", len(state.Stmts), maxSessionStmts)
	}
	if !state.PinnedBy.Has(PinUntrackedState) {
		t.Error("a session past the cap is still considered movable, so a backend " +
			"switch would replay it missing statements it prepared")
	}
	// Re-preparing a known name must still update at the cap.
	h.TrackPreparedStatement(state, parseMsg("s0", "SELECT 'updated'"))
	if got := state.Stmts["s0"]; got != "SELECT 'updated'" {
		t.Errorf("s0 = %q after re-Parse at the cap; the update was dropped", got)
	}
}

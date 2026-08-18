package protocol

import "testing"

// Session variables are replayed onto a new connection after a backend switch.
// A form that is not parsed is a setting the client asked for and silently does
// not get.
func TestSetFormsAreTracked(t *testing.T) {
	for _, tc := range []struct {
		sql      string
		key      string
		wantKept bool
	}{
		{"SET search_path = public", "search_path", true},
		{"SET search_path TO public", "search_path", true},
		{"SET search_path=public", "search_path", true},
		{"SET application_name='reporting'", "application_name", true},
		{"set TimeZone = 'UTC'", "timezone", true},
		{"SET SESSION statement_timeout = '5s'", "statement_timeout", true},
		{"SET TIME ZONE 'UTC'", "timezone", true},
		{"SET ROLE readonly", "role", true},
		{"SET SESSION AUTHORIZATION alice", "session_authorization", true},
		// SET LOCAL ends with the transaction. A session inside a transaction
		// is never released, so a new connection has no use for it — and
		// replaying it outside one is not what the client asked for.
		{"SET LOCAL work_mem = '64MB'", "work_mem", false},
		{"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", "transaction", false},
		{"SET CONSTRAINTS ALL DEFERRED", "constraints", false},
	} {
		state := &SessionState{}
		h := NewPostgresHandler()
		h.TrackSessionState(state, simpleQuery(tc.sql))

		_, kept := state.Vars[tc.key]
		if kept != tc.wantKept {
			t.Errorf("%q -> tracked keys %v; wanted key %q present=%v",
				tc.sql, keysOf(state.Vars), tc.key, tc.wantKept)
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Two different SET SESSION statements are two different settings.
//
// The old parser split on whitespace and took the first field as the key, so
// both of these were stored under "session" and the second silently replaced
// the first — the session was then replayed missing a setting it had asked for.
func TestSetSessionStatementsDoNotCollide(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	h.TrackSessionState(state, simpleQuery("SET SESSION statement_timeout = '5s'"))
	h.TrackSessionState(state, simpleQuery("SET SESSION lock_timeout = '2s'"))

	if len(state.Vars) != 2 {
		t.Fatalf("tracked %d settings from two distinct SET SESSION statements: %v",
			len(state.Vars), state.Vars)
	}
}

// Replay writes back exactly what the client sent, so the stored text has to be
// the statement itself rather than a reconstruction of it.
func TestStoredSetIsTheStatementItself(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	const sent = "SET search_path=public,extensions"
	h.TrackSessionState(state, simpleQuery(sent))

	if got := state.Vars["search_path"]; got != sent {
		t.Errorf("stored %q, want the statement as sent: %q", got, sent)
	}
}

// A SET that cannot be named cannot be replayed, so the session must stop being
// movable rather than be moved without it.
func TestUnparseableSetPinsTheSession(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	h.TrackSessionState(state, simpleQuery("SET \"quoted param\" = 1"))

	if !state.PinnedBy.Has(PinUntrackedState) {
		t.Error("a SET Pontus could not parse left the session movable; a backend " +
			"switch would drop the setting silently")
	}
}

// A plain query must not be mistaken for an unreadable SET — that would pin
// every session immediately.
func TestNonSetDoesNotPin(t *testing.T) {
	h := NewPostgresHandler()
	state := &SessionState{}

	for _, sql := range []string{"SELECT 1", "INSERT INTO t VALUES (1)", "SHOW search_path"} {
		h.TrackSessionState(state, simpleQuery(sql))
	}
	if state.PinnedBy != 0 {
		t.Errorf("ordinary queries pinned the session: %v", state.PinnedBy)
	}
}

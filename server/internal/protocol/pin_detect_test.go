package protocol

import "testing"

// The rule this replaces was a substring search for "temp ", so any query with
// a column named `temp` pinned its session to one backend connection — for
// life, because nothing ever cleared the flag.
func TestPinReasonFor(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  PinReason
	}{
		// The false positives that made pooling collapse.
		{"SELECT temp FROM sensors", 0},
		{"SELECT temperature, temp FROM readings WHERE temp > 20", 0},
		{"UPDATE jobs SET status = 'temp' WHERE id = 1", 0},
		{"SELECT * FROM temp_readings", 0},
		{"INSERT INTO audit (note) VALUES ('temporary outage')", 0},

		// What genuinely pins.
		{"LISTEN events", PinListen},
		{"listen events", PinListen},
		{"LOCK TABLE accounts IN EXCLUSIVE MODE", PinLock},
		{"CREATE TEMP TABLE staging (id int)", PinTempTable},
		{"CREATE TEMPORARY TABLE staging (id int)", PinTempTable},
		{"CREATE GLOBAL TEMPORARY TABLE staging (id int)", PinTempTable},
		{"create local temp table staging (id int)", PinTempTable},
		{"CREATE TEMP SEQUENCE s", PinTempTable},
		{"DECLARE c CURSOR WITH HOLD FOR SELECT 1", PinCursor},
		{"SELECT pg_advisory_lock(42)", PinAdvisoryLock},

		// Near misses that must not pin.
		{"CREATE TABLE staging (id int)", 0},
		{"CREATE TABLE temp_report (id int)", 0},
		{"DECLARE c CURSOR FOR SELECT 1", 0},
		{"SELECT pg_advisory_xact_lock(42)", 0},
		{"SELECT 1", 0},
	} {
		if got := pinReasonFor([]byte(tc.query)); got != tc.want {
			t.Errorf("pinReasonFor(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestUnpinReasonFor(t *testing.T) {
	all := ^PinReason(0)
	for _, tc := range []struct {
		query string
		want  PinReason
	}{
		{"DISCARD ALL", all},
		{"discard all;", all},
		{"DISCARD TEMP", PinTempTable},
		{"UNLISTEN *", PinListen},
		{"CLOSE ALL", PinCursor},

		// UNLISTEN of one channel may leave others registered, and Pontus does
		// not track which — so it cannot claim the reason is gone.
		{"UNLISTEN events", 0},
		{"DISCARD PLANS", 0},
		{"SELECT 1", 0},
	} {
		if got := unpinReasonFor([]byte(tc.query)); got != tc.want {
			t.Errorf("unpinReasonFor(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

// A pin has to be liftable, or the connection is gone for the session's life.
func TestPinsAreLifted(t *testing.T) {
	h := &PostgresHandler{}
	state := &SessionState{}

	h.TrackSessionState(state, simpleQuery("LISTEN events"))
	h.TrackSessionState(state, simpleQuery("CREATE TEMP TABLE t (id int)"))
	if !h.IsPinned(state) {
		t.Fatal("LISTEN and a temp table did not pin the session")
	}

	h.TrackSessionState(state, simpleQuery("UNLISTEN *"))
	if !state.PinnedBy.Has(PinTempTable) {
		t.Error("UNLISTEN * cleared the temp-table pin, which it does not affect")
	}
	if state.PinnedBy.Has(PinListen) {
		t.Error("UNLISTEN * did not clear the LISTEN pin")
	}

	h.TrackSessionState(state, simpleQuery("DISCARD ALL"))
	if h.IsPinned(state) {
		t.Errorf("DISCARD ALL left the session pinned by %v", state.PinnedBy)
	}
}

// A LOCK is held until the transaction ends, and nothing on the request path
// marks that end.
func TestTransactionPinsClearAtTheBoundary(t *testing.T) {
	h := &PostgresHandler{}
	state := &SessionState{}

	h.TrackSessionState(state, simpleQuery("LOCK TABLE accounts"))
	h.TrackSessionState(state, simpleQuery("LISTEN events"))

	h.ReleaseTransactionPins(state)

	if state.PinnedBy.Has(PinLock) {
		t.Error("the LOCK pin survived the transaction that held it")
	}
	if !state.PinnedBy.Has(PinListen) {
		t.Error("ending a transaction cleared a LISTEN, which outlives it")
	}
}

// The map is keyed by client input and replayed on every backend switch, so it
// needs a cap — and a session that overruns it must stop being movable rather
// than be replayed with settings missing.
func TestSessionVarsAreBounded(t *testing.T) {
	h := &PostgresHandler{}
	state := &SessionState{}

	for i := range maxSessionVars * 4 {
		h.TrackSessionState(state, simpleQuery("SET var"+itoa(i)+" = 1"))
	}

	if len(state.Vars) > maxSessionVars {
		t.Errorf("tracked %d variables, cap is %d", len(state.Vars), maxSessionVars)
	}
	if !state.PinnedBy.Has(PinUntrackedState) {
		t.Error("a session past the cap is still considered movable, so a " +
			"backend switch would silently drop the settings it asked for")
	}

	// A variable already tracked must still update at the cap, or the replayed
	// value goes stale.
	h.TrackSessionState(state, simpleQuery("SET var0 = 99"))
	if got := state.Vars["var0"]; got != "= 99" {
		t.Errorf("var0 = %q after re-SET at the cap; the update was dropped", got)
	}
}

func simpleQuery(sql string) []byte {
	out := make([]byte, 5, 5+len(sql)+1)
	out[0] = 'Q'
	length := 4 + len(sql) + 1
	out[1], out[2], out[3], out[4] = byte(length>>24), byte(length>>16), byte(length>>8), byte(length)
	out = append(out, sql...)
	return append(out, 0)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

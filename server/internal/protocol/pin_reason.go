package protocol

// PinReason records *why* a session is tied to one backend connection, so each
// reason can be lifted independently once it stops applying.
//
// A single boolean could only ever be set. Nothing ever cleared it, so the
// first statement that tripped the check held that connection for the life of
// the session — and the check was a substring match on "temp ", so
// `SELECT temp FROM sensors` tripped it. A pool whose connections are pinned
// one at a time and never given back drains to nothing under ordinary traffic,
// which is the failure mode a pooler exists to prevent.
type PinReason uint16

const (
	// PinListen — LISTEN registers interest on one specific backend
	// connection and notifications arrive nowhere else. Cleared by
	// `UNLISTEN *` or `DISCARD ALL`.
	PinListen PinReason = 1 << iota

	// PinTempTable — a temporary table lives in its connection's temp schema
	// and is invisible from any other. Cleared by `DISCARD ALL` or
	// `DISCARD TEMP`.
	PinTempTable

	// PinCursor — a WITH HOLD cursor outlives its transaction but not its
	// connection. Cleared by `CLOSE ALL` or `DISCARD ALL`.
	PinCursor

	// PinLock — an explicit LOCK is held until the transaction ends, so this
	// reason clears itself at the next transaction boundary rather than
	// needing a statement to lift it.
	PinLock

	// PinAdvisoryLock — a session-level advisory lock is held on the
	// connection until it is released or the session ends. Detected because
	// losing one silently is worse than pinning a session that only mentioned
	// the function name: an advisory lock released by a connection going back
	// to the pool is a mutual-exclusion failure in the client's application.
	PinAdvisoryLock

	// PinUntrackedState — the session carries more state than Pontus is
	// willing to remember, so it can no longer be replayed onto another
	// connection faithfully. See maxSessionVars.
	PinUntrackedState
)

// transactionScoped are the reasons that end with the transaction holding them.
const transactionScoped = PinLock

// Has reports whether any of the given reasons are set.
func (r PinReason) Has(other PinReason) bool { return r&other != 0 }

// String names the reasons, for logs and metrics. Fixed vocabulary, so it is
// safe as a metric label.
func (r PinReason) String() string {
	if r == 0 {
		return "none"
	}
	names := [...]struct {
		reason PinReason
		name   string
	}{
		{PinListen, "listen"},
		{PinTempTable, "temp_table"},
		{PinCursor, "hold_cursor"},
		{PinLock, "lock"},
		{PinAdvisoryLock, "advisory_lock"},
		{PinUntrackedState, "untracked_state"},
	}

	out := make([]byte, 0, 32)
	for _, n := range names {
		if !r.Has(n.reason) {
			continue
		}
		if len(out) > 0 {
			out = append(out, ',')
		}
		out = append(out, n.name...)
	}
	return string(out)
}

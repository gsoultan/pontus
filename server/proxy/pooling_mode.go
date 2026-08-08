package proxy

import (
	"strings"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

// poolingMode decides when a backend connection goes back to the pool.
//
// gpool answers "where does a connection come from and how many are there".
// This answers "how long does one client get to keep it" — they are different
// questions, and only the proxy can answer the second because only it sees the
// transaction boundaries.
//
// The setting was configurable and exposed in the dashboard for a long time
// while nothing read it: transaction pooling was hardcoded at the release site,
// so an operator selecting "session" silently got transaction pooling.
type poolingMode int

const (
	// poolTransaction returns the connection at each transaction boundary.
	// Highest multiplexing, and the default.
	poolTransaction poolingMode = iota

	// poolSession keeps the connection for the client's whole session. Session
	// state — SET, LISTEN, temp tables, session-level prepared statements —
	// simply works, at the cost of one backend connection per client.
	poolSession

	// poolStatement returns the connection after every statement. Multi
	// statement transactions cannot be supported, so they are refused rather
	// than silently split across connections.
	poolStatement
)

func parsePoolingMode(name string) poolingMode {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "session":
		return poolSession
	case "statement":
		return poolStatement
	default:
		return poolTransaction
	}
}

func (m poolingMode) String() string {
	switch m {
	case poolSession:
		return "session"
	case poolStatement:
		return "statement"
	default:
		return "transaction"
	}
}

// shouldRelease reports whether the connection may go back to the pool now.
//
// A pinned session is never released regardless of mode: LISTEN, advisory locks
// and temp tables all bind state to one backend connection, and handing that
// connection to another client loses it.
func (m poolingMode) shouldRelease(state *protocol.SessionState, pinned bool) bool {
	if state == nil || pinned {
		return false
	}
	if m == poolSession {
		// Released when the client disconnects, not before.
		return false
	}
	// Transaction and statement modes both wait for the transaction to close.
	// They differ in whether a transaction may be opened at all, which is
	// enforced separately.
	return state.TxState == protocol.StateIdle
}

// rejectsTransactions reports whether opening a transaction is an error in this
// mode. Statement pooling cannot hold a connection across statements, so a
// transaction spanning them would silently execute on different backends.
func (m poolingMode) rejectsTransactions() bool {
	return m == poolStatement
}

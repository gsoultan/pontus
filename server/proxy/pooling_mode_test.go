package proxy

import (
	"testing"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

func TestParsePoolingMode(t *testing.T) {
	cases := map[string]poolingMode{
		"":            poolTransaction,
		"transaction": poolTransaction,
		"session":     poolSession,
		"statement":   poolStatement,
		"  Session  ": poolSession,
		"STATEMENT":   poolStatement,
		"nonsense":    poolTransaction,
	}

	for input, want := range cases {
		if got := parsePoolingMode(input); got != want {
			t.Errorf("parsePoolingMode(%q) = %v, want %v", input, got, want)
		}
	}
}

// The mode was configurable, shown in the dashboard, and read by nothing:
// transaction pooling was hardcoded. Each mode must now behave differently.
func TestShouldReleaseHonoursTheMode(t *testing.T) {
	idle := &protocol.SessionState{TxState: protocol.StateIdle}
	inTx := &protocol.SessionState{TxState: protocol.StateError}

	cases := []struct {
		name   string
		mode   poolingMode
		state  *protocol.SessionState
		pinned bool
		want   bool
	}{
		{"transaction releases when idle", poolTransaction, idle, false, true},
		{"transaction holds mid-transaction", poolTransaction, inTx, false, false},
		{"statement releases when idle", poolStatement, idle, false, true},
		{"session never releases mid-session", poolSession, idle, false, false},
		{"session holds mid-transaction too", poolSession, inTx, false, false},

		// Pinning outranks every mode: LISTEN, advisory locks and temp tables
		// bind state to one backend connection.
		{"pinned is never released, transaction", poolTransaction, idle, true, false},
		{"pinned is never released, statement", poolStatement, idle, true, false},

		{"nil state is never released", poolTransaction, nil, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mode.shouldRelease(tc.state, tc.pinned); got != tc.want {
				t.Errorf("shouldRelease = %v, want %v", got, tc.want)
			}
		})
	}
}

// Session mode must differ from transaction mode for an idle session — that is
// the entire point of the setting.
func TestSessionModeDiffersFromTransactionMode(t *testing.T) {
	idle := &protocol.SessionState{TxState: protocol.StateIdle}

	if poolTransaction.shouldRelease(idle, false) == poolSession.shouldRelease(idle, false) {
		t.Fatal("session and transaction pooling behave identically; the setting does nothing")
	}
}

func TestOnlyStatementModeRejectsTransactions(t *testing.T) {
	if !poolStatement.rejectsTransactions() {
		t.Error("statement pooling must refuse transactions; it cannot hold a connection across statements")
	}
	if poolTransaction.rejectsTransactions() || poolSession.rejectsTransactions() {
		t.Error("only statement pooling refuses transactions")
	}
}

// Both release sites must go through the same rule.
//
// The predicate was correct and session pooling still never held a connection:
// executeRequest released on an idle transaction unconditionally while the
// transaction loop honoured the mode, and executeRequest runs first. Testing
// the helper in isolation proved nothing about it being the only thing that
// releases, so this asserts the value-based form both sites now share.
func TestShouldReleaseAtMatchesTheSessionStateForm(t *testing.T) {
	states := []protocol.TransactionState{protocol.StateIdle, protocol.StateError}

	for _, mode := range []poolingMode{poolTransaction, poolSession, poolStatement} {
		for _, txState := range states {
			for _, pinned := range []bool{false, true} {
				viaState := mode.shouldRelease(&protocol.SessionState{TxState: txState}, pinned)
				viaValue := mode.shouldReleaseAt(txState, pinned)
				if viaState != viaValue {
					t.Errorf("mode=%v txState=%v pinned=%v: shouldRelease=%v shouldReleaseAt=%v; "+
						"the two release sites would disagree", mode, txState, pinned, viaState, viaValue)
				}
			}
		}
	}
}

// Session mode must differ from transaction mode at an idle transaction — the
// exact condition executeRequest was releasing on regardless of mode.
func TestSessionModeHoldsAtIdleTransaction(t *testing.T) {
	if poolSession.shouldReleaseAt(protocol.StateIdle, false) {
		t.Error("session pooling released at an idle transaction; it must hold until the client disconnects")
	}
	if !poolTransaction.shouldReleaseAt(protocol.StateIdle, false) {
		t.Error("transaction pooling did not release at an idle transaction")
	}
}

func TestPoolingModeString(t *testing.T) {
	for mode, want := range map[poolingMode]string{
		poolTransaction: "transaction",
		poolSession:     "session",
		poolStatement:   "statement",
	} {
		if got := mode.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}

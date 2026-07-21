package protocol

// SessionState tracks session-level database configuration.
type SessionState struct {
	Vars    map[string]string
	Stmts   map[string]string // name -> query
	TxState TransactionState
	User    string
	LastLSN string // Log Sequence Number for consistency tracking
	Pinned  bool   // If true, the session must not be unpooled (e.g., LISTEN, Temp Tables)
}

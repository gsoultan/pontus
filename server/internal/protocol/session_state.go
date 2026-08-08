package protocol

// SessionState tracks session-level database configuration.
type SessionState struct {
	// StartupPacket is the client's raw StartupMessage, retained only when the
	// connection turns out to be a replication stream. The CDC path has to
	// forward the original bytes to the node holding the slot, and the packet
	// has already been consumed from the client by the time that is known.
	StartupPacket []byte

	// Replication is the raw "replication" startup parameter. A non-empty,
	// non-false value means the client asked for a replication stream, which
	// cannot be pooled — see IsReplication.
	Replication string
	Vars        map[string]string
	Stmts       map[string]string // name -> query
	TxState     TransactionState
	User        string
	// Database is the database named in the client's startup packet. It is part
	// of a cache key's identity: the same SQL against a different database is a
	// different result.
	Database string
	LastLSN  string // Log Sequence Number for consistency tracking
	Pinned   bool   // If true, the session must not be unpooled (e.g., LISTEN, Temp Tables)
}

package protocol

// TransactionState represents the current state of a database transaction.
type TransactionState int

const (
	StateIdle TransactionState = iota
	StateInTransaction
	StateError
	StatePartial
)

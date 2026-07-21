package protocol

// QueryInfo contains metadata about a database query.
type QueryInfo struct {
	ReadOnly       bool
	InTransaction  bool
	AffectedTables []string
}

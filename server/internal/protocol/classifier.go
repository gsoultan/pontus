package protocol

// QueryClassifier identifies and normalizes database queries.
type QueryClassifier interface {
	// ClassifyQuery identifies if the given query is read-only.
	ClassifyQuery(data []byte) QueryInfo

	// NormalizeQuery strips values from the query for metrics grouping.
	NormalizeQuery(data []byte) string

	// IsTerminate reports whether this message ends the client's session.
	//
	// It must not be forwarded. The backend connection belongs to Pontus, not to
	// the client that borrowed it: passing the client's goodbye through closes a
	// connection the pool was about to reuse, which is why every client session
	// used to cost a fresh one.
	IsTerminate(data []byte) bool

	// Cacheable reports whether a response to this message may be stored and
	// replayed to another client.
	//
	// Only a self-contained request/response exchange qualifies. PostgreSQL's
	// extended protocol splits one query across Parse, Bind and Execute, and the
	// reply to a Parse is ParseComplete — not a result set. Serving a stored
	// result set in its place desynchronises the connection: the client then
	// Binds a statement the server never parsed and gets
	// "prepared statement ... does not exist" (SQLSTATE 26000).
	Cacheable(data []byte) bool
}

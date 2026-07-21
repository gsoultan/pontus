package protocol

// QueryClassifier identifies and normalizes database queries.
type QueryClassifier interface {
	// ClassifyQuery identifies if the given query is read-only.
	ClassifyQuery(data []byte) QueryInfo

	// NormalizeQuery strips values from the query for metrics grouping.
	NormalizeQuery(data []byte) string
}

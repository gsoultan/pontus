package protocol

import (
	"slices"
	"strings"
	"testing"
)

func query(sql string) []byte {
	b := []byte{'Q', 0, 0, 0, 0}
	b = append(b, sql...)
	return append(b, 0)
}

// A write that contains a nested SELECT is still a write. Classifying these as
// read-only routed them to a replica and made them eligible for caching.
func TestClassifyQuery_NestedSelectStaysWrite(t *testing.T) {
	h := NewPostgresHandler()

	for _, sql := range []string{
		"INSERT INTO audit SELECT * FROM users",
		"UPDATE t SET x = (SELECT max(id) FROM u) WHERE id = 1",
		"DELETE FROM t WHERE id IN (SELECT id FROM u)",
		"SELECT 1; DROP TABLE users",
		"WITH c AS (SELECT 1) INSERT INTO t SELECT * FROM c",
		"SELECT * FROM t FOR UPDATE",
	} {
		if info := h.ClassifyQuery(query(sql)); info.ReadOnly {
			t.Errorf("classified as read-only, must be a write: %s", sql)
		}
	}
}

func TestClassifyQuery_ReadsStayReads(t *testing.T) {
	h := NewPostgresHandler()

	for _, sql := range []string{
		"SELECT * FROM users WHERE id = 1",
		"SELECT a, b FROM t JOIN u ON t.id = u.id",
		"SHOW search_path",
		"EXPLAIN SELECT 1",
	} {
		if info := h.ClassifyQuery(query(sql)); !info.ReadOnly {
			t.Errorf("classified as a write, must be read-only: %s", sql)
		}
	}
}

// Table-level cache invalidation cannot fire for a table the classifier never
// recorded, so the write targets are the part that has to be right.
func TestClassifyQuery_RecordsWriteTargets(t *testing.T) {
	h := NewPostgresHandler()

	cases := map[string]string{
		"INSERT INTO orders (id) VALUES (1)": "orders",
		"UPDATE orders SET x = 1":            "orders",
		"DELETE FROM orders WHERE id = 1":    "orders",
		"TRUNCATE TABLE orders":              "orders",
		"SELECT * FROM orders":               "orders",
	}

	for sql, want := range cases {
		info := h.ClassifyQuery(query(sql))
		if !slices.Contains(info.AffectedTables, want) {
			t.Errorf("%s: affected tables %v, want to contain %q", sql, info.AffectedTables, want)
		}
	}
}

// Digits inside an identifier are part of the name. Collapsing them made two
// different tables share one cache key.
func TestNormalizeQuery_KeepsIdentifierDigits(t *testing.T) {
	h := NewPostgresHandler()

	a := h.NormalizeQuery(query("SELECT * FROM tenant1_orders"))
	b := h.NormalizeQuery(query("SELECT * FROM tenant2_orders"))
	if a == b {
		t.Errorf("distinct tables normalized to the same key: %q", a)
	}

	// Literals must still collapse, or the cache degenerates to one entry per value.
	c := h.NormalizeQuery(query("SELECT * FROM t WHERE id = 1"))
	d := h.NormalizeQuery(query("SELECT * FROM t WHERE id = 2"))
	if c != d {
		t.Errorf("literals did not normalize: %q vs %q", c, d)
	}
}

// The firewall inspects the normalized text while the backend receives the raw
// bytes, so anything normalization drops is invisible to the WAF.
func TestNormalizeQuery_UnbalancedQuoteKeepsTail(t *testing.T) {
	h := NewPostgresHandler()

	got := h.NormalizeQuery(query("SELECT 'x || 1 UNION SELECT * FROM pg_shadow"))
	if strings.TrimSpace(got) == "SELECT" || !strings.Contains(got, "?") {
		t.Errorf("normalization swallowed the statement: %q", got)
	}
}

func TestTokenize_ClassifierKeywordsAreKeywords(t *testing.T) {
	// Every word ClassifyQuery switches on must be emitted as a keyword, or the
	// switch case is unreachable.
	for _, kw := range []string{
		"DROP", "TRUNCATE", "ALTER", "GRANT", "REVOKE", "SHOW", "EXPLAIN",
		"INTO", "TABLE", "CREATE", "MERGE", "COPY", "WITH",
	} {
		toks := slices.Collect(Tokenize(kw))
		if len(toks) != 1 || toks[0].Type != TokenKeyword {
			t.Errorf("%q is not tokenized as a keyword", kw)
		}
	}
}

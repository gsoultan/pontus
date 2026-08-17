package middleware

import (
	"testing"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

// session builds one carrying both the executed bytes and the normalized form,
// because the key is derived from the former and the latter is only for
// metrics. A helper that set only Normalized would test a key nothing uses.
func session(user, database, sql string, vars map[string]string) *Session {
	return &Session{
		Data:       append(append([]byte{'Q', 0, 0, 0, 0}, sql...), 0),
		Normalized: sql,
		State: &protocol.SessionState{
			User:     user,
			Database: database,
			Vars:     vars,
		},
	}
}

// Two sessions sending byte-identical SQL must not share a cache entry unless
// every input that can change the answer is identical too. Keying on the query
// text alone let whichever session missed first decide what the others saw.
func TestCacheKey_SeparatesIdentities(t *testing.T) {
	base := session("alice", "shop", "SELECT * FROM orders", nil)

	cases := map[string]*Session{
		"different user":     session("bob", "shop", "SELECT * FROM orders", nil),
		"different database": session("alice", "warehouse", "SELECT * FROM orders", nil),
		"different query":    session("alice", "shop", "SELECT * FROM invoices", nil),
		"different search_path": session("alice", "shop", "SELECT * FROM orders",
			map[string]string{"search_path": "tenant_b"}),
		"different role": session("alice", "shop", "SELECT * FROM orders",
			map[string]string{"role": "auditor"}),
	}

	baseKey := CacheKey(base)
	for name, other := range cases {
		if CacheKey(other) == baseKey {
			t.Errorf("%s: shares a cache key with the base session", name)
		}
	}
}

// The key still has to be stable, or nothing is ever a hit.
func TestCacheKey_StableForSameIdentity(t *testing.T) {
	a := session("alice", "shop", "SELECT * FROM orders",
		map[string]string{"search_path": "public", "role": "reader"})
	b := session("alice", "shop", "SELECT * FROM orders",
		map[string]string{"role": "reader", "search_path": "public"}) // insertion order differs

	if CacheKey(a) != CacheKey(b) {
		t.Error("same identity produced different keys — map iteration order leaked into the key")
	}
}

// Concatenating fields without a separator lets different identities collide.
func TestCacheKey_NoBoundaryCollision(t *testing.T) {
	a := session("ab", "c", "q", nil)
	b := session("a", "bc", "q", nil)

	if CacheKey(a) == CacheKey(b) {
		t.Error("field boundaries collide: \"ab\"+\"c\" keyed the same as \"a\"+\"bc\"")
	}
}

// Two queries differing only in a literal must not share a cache entry.
//
// The key used the *normalized* query, which exists to group queries for
// metrics and therefore replaces literals with placeholders. `WHERE id = 1` and
// `WHERE id = 2` became the same entry, so the second was answered with the
// first one's rows. Where a literal carries a tenant id, that is one tenant
// reading another's data.
func TestCacheKeyDistinguishesLiterals(t *testing.T) {
	first := &Session{
		Data:       []byte("Q\x00\x00\x00\x00SELECT * FROM users WHERE id = 1\x00"),
		Normalized: "SELECT * FROM users WHERE id = ?",
		State:      &protocol.SessionState{User: "app", Database: "main"},
	}
	second := &Session{
		Data:       []byte("Q\x00\x00\x00\x00SELECT * FROM users WHERE id = 2\x00"),
		Normalized: "SELECT * FROM users WHERE id = ?",
		State:      &protocol.SessionState{User: "app", Database: "main"},
	}

	if CacheKey(first) == CacheKey(second) {
		t.Fatal("two queries differing only in a literal share a cache entry; " +
			"the second would be answered with the first one's rows")
	}
}

// The same statement really should hit.
func TestCacheKeyMatchesAnIdenticalQuery(t *testing.T) {
	build := func() *Session {
		return &Session{
			Data:       []byte("Q\x00\x00\x00\x00SELECT 1\x00"),
			Normalized: "SELECT ?",
			State:      &protocol.SessionState{User: "app", Database: "main"},
		}
	}
	if CacheKey(build()) != CacheKey(build()) {
		t.Error("an identical query does not hit its own cache entry")
	}
}

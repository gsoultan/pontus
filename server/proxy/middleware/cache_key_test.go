package middleware

import (
	"testing"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

func session(user, database, normalized string, vars map[string]string) *Session {
	return &Session{
		Normalized: normalized,
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

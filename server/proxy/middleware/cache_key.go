package middleware

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
)

// CacheKey builds the key a result set is stored under.
//
// The query text alone is not a key. Two sessions sending byte-identical SQL can
// legitimately get different rows — different backend, different database,
// different user under row-level security, different `search_path` resolving the
// same unqualified name to a different table, a different `role`. Keying on the
// text alone means whichever session missed first decides what every other
// session sees, which is a cross-tenant data leak rather than a cache.
//
// So the key is the query *plus* everything that can change its answer. Session
// variables are included wholesale rather than by an allowlist: an allowlist has
// to be right about every GUC that can affect a result, and being wrong is
// silent. A conservative key costs hit rate; a wrong one leaks data.
func CacheKey(s *Session) string {
	h := sha256.New()

	write := func(part string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		h.Write(n[:]) // length-prefixed so "a"+"bc" cannot collide with "ab"+"c"
		h.Write([]byte(part))
	}

	backend := ""
	if s.Backend != nil {
		backend = s.Backend.Address()
	}
	write(backend)

	if s.State != nil {
		write(s.State.Database)
		write(s.State.User)

		keys := make([]string, 0, len(s.State.Vars))
		for k := range s.State.Vars {
			keys = append(keys, k)
		}
		slices.Sort(keys) // map order is random; the key must not be
		for _, k := range keys {
			write(k)
			write(s.State.Vars[k])
		}
	} else {
		write("")
		write("")
	}

	// The bytes that were actually executed, not the normalized form.
	//
	// Normalization strips literals so queries can be grouped for metrics:
	// `WHERE id = 1` and `WHERE id = 2` both become `WHERE id = ?`. Keying a
	// *result* cache on that made them the same entry, so the second query was
	// answered with the first one's rows — a cross-record data leak, and in any
	// application where a tenant id is a literal, a cross-tenant one.
	//
	// This file namespaces the key by backend, database, user and session
	// variables precisely so a response is never reused across identities. It
	// then discarded the values that decide which rows come back.
	write(string(s.Data))

	return string(h.Sum(nil))
}

package protocol

import "bytes"

// Deciding what pins a session, and what lets it go again.
//
// Everything here walks the query token by token without allocating: this runs
// on every statement, and the code it replaces called bytes.ToLower twice per
// query to do a substring search.

// pinReasonFor reports what about this statement ties the session to its
// current backend connection.
func pinReasonFor(query []byte) PinReason {
	verb, rest := nextToken(query)

	switch {
	case equalFold(verb, "listen"):
		return PinListen

	case equalFold(verb, "lock"):
		return PinLock

	case equalFold(verb, "declare"):
		// Only a WITH HOLD cursor outlives its transaction. A plain cursor is
		// gone at commit, so it pins nothing beyond a transaction that is
		// already pinned.
		if containsPhrase(rest, "with", "hold") {
			return PinCursor
		}

	case equalFold(verb, "create"):
		if createsTempObject(rest) {
			return PinTempTable
		}

	case equalFold(verb, "select"), equalFold(verb, "with"):
		// pg_advisory_lock and friends take a *session*-level lock;
		// pg_advisory_xact_lock is transaction-scoped and needs no pin. The
		// name is matched without lowering the query first — this runs on
		// every statement, and allocating a copy of it to do a substring
		// search is what the code being replaced did.
		if containsFold(rest, "pg_advisory_lock") {
			return PinAdvisoryLock
		}
	}

	return 0
}

// unpinReasonFor reports which reasons this statement lifts.
func unpinReasonFor(query []byte) PinReason {
	verb, rest := nextToken(query)
	next, _ := nextToken(rest)

	switch {
	case equalFold(verb, "discard"):
		switch {
		case equalFold(next, "all"):
			// DISCARD ALL is exactly "forget everything about this session",
			// which is every reason there is.
			return ^PinReason(0)
		case equalFold(next, "temp"), equalFold(next, "temporary"):
			return PinTempTable
		}

	case equalFold(verb, "unlisten"):
		// UNLISTEN <channel> may leave other channels registered, and Pontus
		// does not track which. Only the wildcard is known to clear them all.
		if equalFold(next, "*") {
			return PinListen
		}

	case equalFold(verb, "close"):
		if equalFold(next, "all") {
			return PinCursor
		}
	}

	return 0
}

// createsTempObject reports whether a CREATE statement creates something that
// lives in the connection's temporary schema.
//
// The rule this replaces was `bytes.Contains(lower(query), "temp ")`, which
// matched any query with a column, table or alias named `temp` — and, being a
// substring search, matched it anywhere in the statement.
//
// Accepted shapes, per the CREATE TABLE grammar:
//
//	CREATE [ [GLOBAL|LOCAL] {TEMPORARY|TEMP} | UNLOGGED ] TABLE ...
//	CREATE {TEMPORARY|TEMP} {SEQUENCE|VIEW} ...
func createsTempObject(rest []byte) bool {
	token, next := nextToken(rest)

	// The optional scope qualifier. PostgreSQL accepts and ignores both.
	if equalFold(token, "global") || equalFold(token, "local") {
		token, next = nextToken(next)
	}

	if !equalFold(token, "temp") && !equalFold(token, "temporary") {
		return false
	}

	// TEMP must qualify an object that actually lives in the temp schema.
	object, _ := nextToken(next)
	return equalFold(object, "table") ||
		equalFold(object, "sequence") ||
		equalFold(object, "view")
}

// containsFold reports whether a lowercase ASCII literal appears in s, ignoring
// case, without copying s.
func containsFold(s []byte, lower string) bool {
	if len(lower) == 0 || len(s) < len(lower) {
		return false
	}
	for i := 0; i+len(lower) <= len(s); i++ {
		if equalFold(s[i:i+len(lower)], lower) {
			return true
		}
	}
	return false
}

// containsPhrase reports whether the token sequence appears in order and
// adjacent, so "WITH HOLD" does not match a column list mentioning either word
// on its own.
func containsPhrase(s []byte, words ...string) bool {
	rest := s
	for len(rest) > 0 {
		candidate := rest
		matched := true
		for _, word := range words {
			var token []byte
			token, candidate = nextToken(candidate)
			if !equalFold(token, word) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
		_, rest = nextToken(rest)
	}
	return false
}

// nextToken splits off the next token, skipping whitespace and SQL comments.
//
// Comments have to be skipped because query-annotation tools — sqlcommenter and
// the tracing libraries that follow it — prepend one to every statement an ORM
// emits. A `/*app:reports*/ LISTEN events` whose verb went unrecognised left the
// session unpinned, so its connection returned to the pool and the client
// stopped receiving notifications with nothing logged anywhere.
//
// Punctuation that terminates a statement is trimmed, so `DISCARD ALL;` reads
// the same as `DISCARD ALL`.
func nextToken(s []byte) (token, rest []byte) {
	i := skipNoise(s, 0)
	start := i
	for i < len(s) && !isSpace(s[i]) && !startsComment(s, i) {
		i++
	}
	token = bytes.TrimRight(s[start:i], ";\x00")
	return token, s[i:]
}

// skipNoise advances past whitespace and comments.
func skipNoise(s []byte, i int) int {
	for i < len(s) {
		switch {
		case isSpace(s[i]):
			i++
		case startsLineComment(s, i):
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case startsBlockComment(s, i):
			i = skipBlockComment(s, i)
		default:
			return i
		}
	}
	return i
}

// skipBlockComment consumes a /* ... */ comment, honouring nesting.
//
// PostgreSQL block comments nest, unlike C's: `/* a /* b */ c */` is one
// comment. Stopping at the first `*/` would leave ` c */` to be read as
// statement text.
func skipBlockComment(s []byte, i int) int {
	depth := 0
	for i < len(s) {
		switch {
		case startsBlockComment(s, i):
			depth++
			i += 2
		case i+1 < len(s) && s[i] == '*' && s[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	// Unterminated: the rest of the statement is inside the comment.
	return i
}

func startsComment(s []byte, i int) bool {
	return startsLineComment(s, i) || startsBlockComment(s, i)
}

func startsLineComment(s []byte, i int) bool {
	return i+1 < len(s) && s[i] == '-' && s[i+1] == '-'
}

func startsBlockComment(s []byte, i int) bool {
	return i+1 < len(s) && s[i] == '/' && s[i+1] == '*'
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// equalFold compares a token to a lowercase ASCII literal without allocating.
func equalFold(token []byte, lower string) bool {
	if len(token) != len(lower) {
		return false
	}
	for i := range token {
		c := token[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

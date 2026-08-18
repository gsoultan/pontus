package protocol

import "bytes"

// Parsing a SET well enough to replay it onto another connection.
//
// What is stored is the statement itself, not a reconstruction of it. The
// previous scheme kept a key and a value and rebuilt `SET <k> <v>` at replay
// time, which meant every form it had not anticipated was either lost or
// rebuilt wrong:
//
//   - `SET search_path=public` — no spaces around `=` — was not recorded at all,
//     because splitting on whitespace produced a single field.
//   - `SET SESSION statement_timeout = '5s'` was recorded under the key
//     "session", so any two `SET SESSION` statements collided and the first
//     setting was lost.
//   - `SET LOCAL work_mem = ...` was recorded as a session setting, and
//     replaying it outside a transaction is not what the client asked for.
//
// Keeping the original text removes the whole class: replay writes back
// exactly the bytes the client sent. The parsed name is used only to decide
// which statement supersedes which.

// setScope is what a parsed SET applies to.
type setScope uint8

const (
	// scopeSession — replay it onto a new connection.
	scopeSession setScope = iota

	// scopeTransaction — SET LOCAL, SET TRANSACTION, SET CONSTRAINTS. These
	// end with the transaction, and a session inside a transaction is not
	// released, so a new connection never needs them.
	scopeTransaction

	// scopeUnknown — a SET Pontus cannot name. The session must not move,
	// since replaying it would leave the setting behind.
	scopeUnknown

	// scopeNotSet — not a SET at all, and nothing to record. Distinct from
	// scopeUnknown so the caller does not have to re-tokenize the statement to
	// tell "this is not a SET" from "this is a SET I could not read".
	scopeNotSet
)

// parseSet returns the configuration parameter a SET assigns, and whether that
// setting outlives the current transaction.
//
// The name is normalized to lower case; PostgreSQL parameter names are
// case-insensitive, so `SET TimeZone` and `SET timezone` are one setting and
// must not become two entries that replay in an arbitrary order.
func parseSet(query []byte) (name string, scope setScope) {
	verb, rest := nextToken(query)
	if !equalFold(verb, "set") {
		return "", scopeNotSet
	}

	token, after := nextToken(rest)

	// Optional scope qualifier.
	switch {
	case equalFold(token, "local"):
		return "", scopeTransaction
	case equalFold(token, "session"):
		// `SET SESSION AUTHORIZATION x` and `SET SESSION CHARACTERISTICS ...`
		// are their own statements, not a scope followed by a parameter, so
		// the next token has to be inspected before consuming this one.
		token, after = nextToken(after)
	}

	switch {
	case equalFold(token, "transaction"), equalFold(token, "constraints"):
		return "", scopeTransaction

	case equalFold(token, "authorization"):
		// SET SESSION AUTHORIZATION — changes the session user.
		return "session_authorization", scopeSession

	case equalFold(token, "characteristics"):
		// SET SESSION CHARACTERISTICS AS TRANSACTION ... — a session-level
		// default for later transactions, so it does outlive this one.
		return "session_characteristics", scopeSession

	case equalFold(token, "role"):
		// Privilege-bearing: a session replayed without it runs as the wrong
		// role on the new connection.
		return "role", scopeSession

	case equalFold(token, "time"):
		// SET TIME ZONE <value> is spelled differently from every other
		// parameter but is the `timezone` setting.
		if zone, _ := nextToken(after); equalFold(zone, "zone") {
			return "timezone", scopeSession
		}
		return "", scopeUnknown
	}

	// The ordinary form. The parameter name may be jammed against the operator
	// — `SET search_path=public` is valid — so the token is cut at the first
	// `=` rather than assumed to be the whole of it.
	if i := bytes.IndexByte(token, '='); i >= 0 {
		token = token[:i]
	}
	if len(token) == 0 || !isIdentifier(token) {
		return "", scopeUnknown
	}
	return lowerASCII(token), scopeSession
}

// isIdentifier reports whether a token could be a configuration parameter name.
//
// Anything else means the statement was not the shape assumed here, and
// guessing at it is what produced the "session" key that swallowed two
// different settings.
func isIdentifier(token []byte) bool {
	for _, c := range token {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '_', c == '.', c == '$':
		default:
			return false
		}
	}
	return true
}

func lowerASCII(token []byte) string {
	out := make([]byte, len(token))
	for i, c := range token {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

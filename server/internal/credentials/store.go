package credentials

import "context"

// Store answers "what is this role's stored password verifier".
//
// One method, because there is exactly one question. The two implementations
// differ only in where the answer comes from: an auth_query against the
// database, or a static file for deployments that will not grant even the
// read that query needs.
type Store interface {
	// Lookup returns the verifier for a role, or ErrUnknownUser.
	//
	// The user name comes from a client's startup packet and is therefore
	// untrusted input: an implementation must neither interpolate it into SQL
	// nor let an unbounded number of distinct values accumulate.
	Lookup(ctx context.Context, user string) (Verifier, error)
}

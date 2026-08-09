package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DefaultAuthQuery reads a role's verifier.
//
// pg_authid is superuser-only, so the documented deployment is a SECURITY
// DEFINER function owned by a superuser and executable by an ordinary
// auth_user — never "make the admin connection a superuser". The query is
// configurable precisely so that function can be named instead.
const DefaultAuthQuery = `SELECT rolname, coalesce(rolpassword, '') FROM pg_authid WHERE rolname = $1`

// Row is one result row. Its own interface rather than *sql.Row because a
// zero-valued sql.Row blocks forever in Scan, so the concrete type cannot be
// faked — a test double would hang instead of failing.
type Row interface {
	Scan(dest ...any) error
}

// Querier is the part of a database handle this needs. Narrow on purpose: it
// lets the store be exercised without a database, and it keeps the credential
// path from acquiring a connection pool of its own.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) Row
}

// SQLQuerier adapts anything with database/sql's signature — *sql.DB, or the
// pool's own admin session — to Querier.
type SQLQuerier struct {
	DB interface {
		QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	}
}

func (q SQLQuerier) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	return q.DB.QueryRowContext(ctx, query, args...)
}

// QueryStore reads verifiers from the database over Pontus's own privileged
// connection.
type QueryStore struct {
	db    Querier
	query string
}

// NewQueryStore builds a store. An empty query takes DefaultAuthQuery.
func NewQueryStore(db Querier, query string) (*QueryStore, error) {
	if db == nil {
		return nil, errors.New("auth_query needs a database connection (set admin_dsn)")
	}
	if query == "" {
		query = DefaultAuthQuery
	}
	return &QueryStore{db: db, query: query}, nil
}

// Lookup runs the auth query for one role.
//
// The user name is a bind parameter, never interpolated. It arrives in a
// startup packet from anyone who can reach the port, so string-building the
// query here would be SQL injection reachable before authentication — the
// worst possible position for it.
func (s *QueryStore) Lookup(ctx context.Context, user string) (Verifier, error) {
	var name, stored string

	err := s.db.QueryRowContext(ctx, s.query, user).Scan(&name, &stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Verifier{}, fmt.Errorf("%w: %q", ErrUnknownUser, user)
	case err != nil:
		return Verifier{}, fmt.Errorf("auth query for %q: %w", user, err)
	}

	// A query that returns a different role than the one asked for is either
	// misconfigured or doing something clever with the argument. Either way the
	// answer is not about this user, and using it would authenticate the wrong
	// person.
	if name != user {
		return Verifier{}, fmt.Errorf("auth query for %q returned %q", user, name)
	}

	return ParseVerifier(stored)
}

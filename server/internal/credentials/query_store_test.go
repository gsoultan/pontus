package credentials

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recordingQuerier captures what SQL and arguments it was handed.
type recordingQuerier struct {
	query string
	args  []any
}

func (q *recordingQuerier) QueryRowContext(_ context.Context, query string, args ...any) Row {
	q.query = query
	q.args = args
	return errRow{}
}

// errRow stands in for a row that cannot be read. A zero-valued sql.Row blocks
// forever in Scan, which is why Querier returns an interface.
type errRow struct{}

func (errRow) Scan(...any) error { return errors.New("no row") }

// The user name arrives in a startup packet, before any authentication has
// happened. Interpolating it into SQL would be injection reachable by anyone
// who can open a socket — the worst possible position for it.
func TestAuthQueryBindsTheUserNameRatherThanInterpolatingIt(t *testing.T) {
	q := &recordingQuerier{}
	s, err := NewQueryStore(q, "")
	if err != nil {
		t.Fatal(err)
	}

	hostile := `alice'; DROP TABLE pg_authid; --`
	_, _ = s.Lookup(context.Background(), hostile)

	if strings.Contains(q.query, hostile) || strings.Contains(q.query, "DROP TABLE") {
		t.Fatalf("the user name was built into the SQL: %q", q.query)
	}
	if len(q.args) != 1 || q.args[0] != hostile {
		t.Errorf("args = %v, want the user name passed as a bind parameter", q.args)
	}
	if !strings.Contains(q.query, "$1") {
		t.Errorf("query %q does not use a placeholder", q.query)
	}
}

func TestNewQueryStoreDefaultsTheQuery(t *testing.T) {
	s, err := NewQueryStore(&recordingQuerier{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.query != DefaultAuthQuery {
		t.Errorf("query = %q, want the default", s.query)
	}
}

// pg_authid is superuser-only, so the deployment story is a SECURITY DEFINER
// function. That only works if the query is configurable.
func TestNewQueryStoreKeepsACustomQuery(t *testing.T) {
	const custom = "SELECT rolname, verifier FROM pontus_auth_lookup($1)"
	s, err := NewQueryStore(&recordingQuerier{}, custom)
	if err != nil {
		t.Fatal(err)
	}
	if s.query != custom {
		t.Errorf("query = %q, want the configured one", s.query)
	}
}

func TestNewQueryStoreNeedsADatabase(t *testing.T) {
	if _, err := NewQueryStore(nil, ""); err == nil {
		t.Error("built an auth_query store with no database connection")
	}
}

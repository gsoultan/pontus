package config

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultAdminDatabase is the database name a client connects to in order to
// reach the administration console.
//
// "pgbouncer" rather than "pontus" on purpose. The console exists so that the
// dashboards, exporters and runbooks a deployment already has keep working
// after Pontus replaces pgbouncer, and every one of them is pointed at this
// name. An operator who wants the Pontus name can set `database:` explicitly.
const DefaultAdminDatabase = "pgbouncer"

// AdminConsole configures the pgbouncer-compatible administration console: a
// virtual database on the proxy port that answers SHOW commands about Pontus
// itself rather than proxying them to a backend.
type AdminConsole struct {
	// Enabled turns the console on. Off by default: it reports pool occupancy,
	// configured backends and client identities, which is reconnaissance for
	// anyone who should not have it.
	Enabled bool `json:"enabled,omitzero" yaml:"enabled"`

	// Database is the name a client connects to in order to reach the console.
	// Empty means DefaultAdminDatabase.
	Database string `json:"database,omitzero" yaml:"database"`

	// Users are the roles allowed in. There is no default and no wildcard: an
	// empty list with the console enabled is a configuration error rather than
	// "everyone", because that is the reading that turns a typo into an open
	// console.
	Users []string `json:"users,omitzero" yaml:"users"`
}

// ErrAdminConsoleNoUsers reports a console that is enabled with nobody allowed
// to use it.
var ErrAdminConsoleNoUsers = errors.New("admin_console.users is empty")

// DatabaseName is the database this console answers on.
func (a *AdminConsole) DatabaseName() string {
	if a == nil || a.Database == "" {
		return DefaultAdminDatabase
	}
	return a.Database
}

// Permits reports whether a role may use the console.
//
// The comparison is exact. PostgreSQL role names are case-sensitive unless they
// were created unquoted, and folding here would let `Admin` reach a console
// configured for `admin` — a widening nobody asked for.
func (a *AdminConsole) Permits(user string) bool {
	if a == nil || !a.Enabled {
		return false
	}
	for _, allowed := range a.Users {
		if allowed == user {
			return true
		}
	}
	return false
}

// Validate reports a console that cannot be served safely.
//
// Called at startup so a misconfiguration is a refusal to boot with the reason,
// rather than a console that silently admits nobody — or, worse, one an
// operator believes is closed.
func (a *AdminConsole) Validate() error {
	if a == nil || !a.Enabled {
		return nil
	}

	var named int
	for _, user := range a.Users {
		if strings.TrimSpace(user) != "" {
			named++
		}
	}
	if named == 0 {
		return fmt.Errorf("%w: the console is enabled but no role may use it; "+
			"list the administrative roles explicitly", ErrAdminConsoleNoUsers)
	}

	for _, user := range a.Users {
		if user == "*" {
			return errors.New(`admin_console.users must not contain "*": ` +
				"it would expose pool and backend inventory to every role that " +
				"can authenticate; list the administrative roles explicitly")
		}
	}
	return nil
}

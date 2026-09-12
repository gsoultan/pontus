package config

import (
	"errors"
	"fmt"
	"strings"
)

// WildcardDatabase matches any database name no rule names explicitly.
//
// pgbouncer spells its fallback the same way. Here it may carry limits but must
// not carry a rewrite: pointing every database a client might name at one real
// database is not a fallback, it is a way to send a tenant's queries to another
// tenant's data.
const WildcardDatabase = "*"

// Database is one client-visible database name and how Pontus serves it —
// pgbouncer's `[databases]` section.
//
// Without this there is one global `max_conns` for every identity on a backend,
// so the ceiling a busy tenant needs is the ceiling every other tenant also
// gets, and the only way to bound one of them is to bound all of them.
type Database struct {
	// Name is what the client puts in its startup packet.
	Name string `json:"name,omitzero" yaml:"name"`

	// Database is the real name on the backend. Empty means Name, which is the
	// ordinary case; setting it is pgbouncer's aliasing, and is what lets a
	// cutover change where `app` points without touching the application.
	Database string `json:"database,omitzero" yaml:"database"`

	// MaxConns is the per-identity ceiling for this database — pgbouncer's
	// per-database `pool_size`. Zero takes the global `max_conns`.
	//
	// Per identity, not per database: a connection carries the credentials it
	// authenticated with, so `(database, user)` is the unit a pool is keyed by
	// and therefore the unit a ceiling applies to.
	MaxConns int32 `json:"max_conns,omitzero" yaml:"max_conns"`
}

// Route is what a client-visible database name resolves to.
type Route struct {
	// Database is the name to open on the backend.
	Database string

	// MaxConns is the per-identity ceiling, or zero for the global one.
	MaxConns int32
}

// ErrDuplicateDatabase reports two rules claiming the same name.
var ErrDuplicateDatabase = errors.New("duplicate database name")

// Databases is the routing table, in configuration order.
type Databases []Database

// Resolve returns the route for a client-visible database name.
//
// An unlisted name resolves to itself with no override, because `databases:` is
// a place to say something different about a database and not a list of the
// ones that are allowed. Making it an allowlist would mean every deployment had
// to enumerate every database before it could set a limit on one.
func (d Databases) Resolve(name string) Route {
	var wildcard *Database
	for i := range d {
		switch d[i].Name {
		case name:
			return d[i].route(name)
		case WildcardDatabase:
			wildcard = &d[i]
		}
	}

	if wildcard != nil {
		// Limits only. The wildcard never rewrites, so an unlisted database
		// still reaches the database it named.
		return Route{Database: name, MaxConns: wildcard.MaxConns}
	}
	return Route{Database: name}
}

func (d Database) route(requested string) Route {
	target := d.Database
	if target == "" {
		target = requested
	}
	return Route{Database: target, MaxConns: d.MaxConns}
}

// Limit returns the per-identity ceiling for a database, or zero for the global
// one. Separated from Resolve because the pool asks only this question, and it
// asks it from a package that should not have to know about aliasing.
func (d Databases) Limit(name string) int32 {
	return d.Resolve(name).MaxConns
}

// Validate reports a routing table that cannot be served as written.
func (d Databases) Validate() error {
	seen := make(map[string]struct{}, len(d))
	for _, entry := range d {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return errors.New("a databases entry has no name")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%w: %q is listed twice", ErrDuplicateDatabase, name)
		}
		seen[name] = struct{}{}

		if name == WildcardDatabase && entry.Database != "" {
			return fmt.Errorf(`the %q database rule must not set "database": `+
				"it would point every unlisted name at %q, sending one tenant's "+
				"queries to another tenant's data; name the databases you mean to rewrite",
				WildcardDatabase, entry.Database)
		}
		if entry.MaxConns < 0 {
			return fmt.Errorf("database %q has a negative max_conns (%d)", name, entry.MaxConns)
		}
	}
	return nil
}

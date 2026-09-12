package config

import (
	"errors"
	"strings"
	"testing"
)

// An unlisted name resolves to itself. `databases:` is a place to say something
// different about a database, not a list of the ones that are allowed —
// treating it as an allowlist would mean enumerating every database in a
// deployment before a limit could be set on one of them.
func TestDatabasesResolveLeavesUnlistedNamesAlone(t *testing.T) {
	routes := Databases{{Name: "app", Database: "app_prod", MaxConns: 20}}

	got := routes.Resolve("reporting")
	if got.Database != "reporting" {
		t.Errorf("unlisted database resolved to %q, want reporting", got.Database)
	}
	if got.MaxConns != 0 {
		t.Errorf("unlisted database took a ceiling of %d, want the global one", got.MaxConns)
	}
}

func TestDatabasesResolveAppliesAnAlias(t *testing.T) {
	routes := Databases{
		{Name: "app", Database: "app_prod", MaxConns: 20},
		{Name: "reporting", MaxConns: 5},
	}

	got := routes.Resolve("app")
	if got.Database != "app_prod" {
		t.Errorf("Database = %q, want app_prod", got.Database)
	}
	if got.MaxConns != 20 {
		t.Errorf("MaxConns = %d, want 20", got.MaxConns)
	}

	// A rule with no `database` keeps the name it was asked for, and carries
	// only its limit.
	got = routes.Resolve("reporting")
	if got.Database != "reporting" {
		t.Errorf("Database = %q, want reporting", got.Database)
	}
	if got.MaxConns != 5 {
		t.Errorf("MaxConns = %d, want 5", got.MaxConns)
	}
}

// The wildcard carries limits and never rewrites, so an unlisted database still
// reaches the database it named.
func TestDatabasesWildcardBoundsWithoutRewriting(t *testing.T) {
	routes := Databases{
		{Name: "app", MaxConns: 50},
		{Name: WildcardDatabase, MaxConns: 5},
	}

	got := routes.Resolve("some_tenant")
	if got.Database != "some_tenant" {
		t.Errorf("wildcard rewrote the database to %q", got.Database)
	}
	if got.MaxConns != 5 {
		t.Errorf("MaxConns = %d, want the wildcard's 5", got.MaxConns)
	}

	// An explicit rule still wins over the wildcard, wherever it is listed.
	if got := routes.Resolve("app"); got.MaxConns != 50 {
		t.Errorf("explicit rule lost to the wildcard: MaxConns = %d, want 50", got.MaxConns)
	}
}

// Pointing every unlisted name at one real database would send one tenant's
// queries to another tenant's data.
func TestDatabasesRefuseAWildcardRewrite(t *testing.T) {
	routes := Databases{{Name: WildcardDatabase, Database: "shared"}}

	err := routes.Validate()
	if err == nil {
		t.Fatal("a wildcard rewrite was accepted")
	}
	if !strings.Contains(err.Error(), "tenant") {
		t.Errorf("error does not explain the danger: %v", err)
	}
}

func TestDatabasesValidateRejectsWhatCannotBeServed(t *testing.T) {
	dup := Databases{{Name: "app"}, {Name: "app", MaxConns: 5}}
	if err := dup.Validate(); !errors.Is(err, ErrDuplicateDatabase) {
		t.Errorf("duplicate names: got %v, want ErrDuplicateDatabase", err)
	}

	unnamed := Databases{{MaxConns: 5}}
	if err := unnamed.Validate(); err == nil {
		t.Error("an entry with no name was accepted")
	}

	negative := Databases{{Name: "app", MaxConns: -1}}
	if err := negative.Validate(); err == nil {
		t.Error("a negative max_conns was accepted")
	}

	ok := Databases{{Name: "app", Database: "app_prod", MaxConns: 20}, {Name: WildcardDatabase, MaxConns: 5}}
	if err := ok.Validate(); err != nil {
		t.Errorf("a valid table was rejected: %v", err)
	}
	if err := Databases(nil).Validate(); err != nil {
		t.Errorf("an empty table was rejected: %v", err)
	}
}

func TestDatabasesLimitIsTheCeilingAlone(t *testing.T) {
	routes := Databases{{Name: "app", Database: "app_prod", MaxConns: 20}}

	if got := routes.Limit("app"); got != 20 {
		t.Errorf("Limit = %d, want 20", got)
	}
	if got := routes.Limit("other"); got != 0 {
		t.Errorf("Limit for an unlisted database = %d, want 0", got)
	}
}

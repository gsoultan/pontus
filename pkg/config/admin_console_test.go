package config

import (
	"errors"
	"strings"
	"testing"
)

// A console enabled with nobody listed reads like "everyone". Failing the boot
// is the only reading that cannot be mistaken for a working configuration.
func TestAdminConsoleRefusesToEnableWithNoUsers(t *testing.T) {
	console := &AdminConsole{Enabled: true}

	err := console.Validate()
	if err == nil {
		t.Fatal("Validate accepted a console that no role may use")
	}
	if !errors.Is(err, ErrAdminConsoleNoUsers) {
		t.Errorf("error = %v, want ErrAdminConsoleNoUsers", err)
	}

	// Whitespace is not a role name.
	console.Users = []string{"", "   "}
	if err := console.Validate(); !errors.Is(err, ErrAdminConsoleNoUsers) {
		t.Errorf("blank names were accepted as roles: %v", err)
	}
}

func TestAdminConsoleRefusesAWildcardUser(t *testing.T) {
	console := &AdminConsole{Enabled: true, Users: []string{"*"}}

	err := console.Validate()
	if err == nil {
		t.Fatal(`Validate accepted "*" as a role`)
	}
	if !strings.Contains(err.Error(), "explicitly") {
		t.Errorf("error does not say what to do instead: %v", err)
	}
}

// A disabled console is not a misconfiguration, whatever else is set.
func TestAdminConsoleValidatesOnlyWhenEnabled(t *testing.T) {
	if err := (&AdminConsole{}).Validate(); err != nil {
		t.Errorf("a disabled console failed validation: %v", err)
	}
	if err := (*AdminConsole)(nil).Validate(); err != nil {
		t.Errorf("an absent console failed validation: %v", err)
	}
}

func TestAdminConsolePermitsOnlyListedRoles(t *testing.T) {
	console := &AdminConsole{Enabled: true, Users: []string{"admin", "ops"}}

	for _, user := range []string{"admin", "ops"} {
		if !console.Permits(user) {
			t.Errorf("Permits(%q) = false, want true", user)
		}
	}
	for _, user := range []string{"app", "", "adm", "admin "} {
		if console.Permits(user) {
			t.Errorf("Permits(%q) = true, want false", user)
		}
	}

	// PostgreSQL role names are case-sensitive unless they were created
	// unquoted. Folding here would let Admin reach a console configured for
	// admin, which is a widening nobody asked for.
	if console.Permits("Admin") {
		t.Error("Permits folded case, admitting a role that was not listed")
	}

	// A disabled console admits nobody, however it is configured.
	console.Enabled = false
	if console.Permits("admin") {
		t.Error("a disabled console admitted a listed role")
	}

	if (*AdminConsole)(nil).Permits("admin") {
		t.Error("an absent console admitted a role")
	}
}

func TestAdminConsoleDefaultsToThePgbouncerDatabaseName(t *testing.T) {
	// The default is pgbouncer rather than pontus because the exporters and
	// runbooks a deployment already has are pointed at that name.
	if got := (&AdminConsole{}).DatabaseName(); got != DefaultAdminDatabase {
		t.Errorf("DatabaseName = %q, want %q", got, DefaultAdminDatabase)
	}
	if got := (*AdminConsole)(nil).DatabaseName(); got != DefaultAdminDatabase {
		t.Errorf("DatabaseName on an absent console = %q, want %q", got, DefaultAdminDatabase)
	}
	if got := (&AdminConsole{Database: "pontus_admin"}).DatabaseName(); got != "pontus_admin" {
		t.Errorf("DatabaseName = %q, want pontus_admin", got)
	}
}

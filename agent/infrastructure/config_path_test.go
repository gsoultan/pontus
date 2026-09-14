package infrastructure

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The allowlist was `strings.HasPrefix(req.FilePath, p)` over the raw request
// field, on a process running as root. Every case below was admitted by it.
func TestResolveConfigPathRefusesWhatIsOutsideTheAllowlist(t *testing.T) {
	allowed := []string{"/var/lib/postgresql", "/etc/postgresql"}

	tests := []struct {
		name      string
		requested string
	}{
		{
			name:      "traversal out of an allowed root",
			requested: "/var/lib/postgresql/../../../etc/cron.d/pwn",
		},
		{
			name:      "traversal to a home directory",
			requested: "/etc/postgresql/../../root/.ssh/authorized_keys",
		},
		{
			name:      "traversal that lands back near the root",
			requested: "/var/lib/postgresql/data/../../../../etc/shadow",
		},
		{
			name:      "a sibling directory sharing the prefix",
			requested: "/var/lib/postgresql-backup/pg_hba.conf",
		},
		{
			name:      "a sibling file sharing the prefix",
			requested: "/var/lib/postgresqlx",
		},
		{
			name:      "a path outside every root",
			requested: "/tmp/pg_hba.conf",
		},
		{
			name:      "a relative path",
			requested: "pg_hba.conf",
		},
		{
			name:      "a relative path that climbs",
			requested: "../../etc/passwd",
		},
		{
			name:      "empty",
			requested: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveConfigPath(tc.requested, allowed)
			if !errors.Is(err, ErrPathNotAllowed) {
				t.Fatalf("resolveConfigPath(%q) = %q, %v; want ErrPathNotAllowed",
					tc.requested, got, err)
			}
			if got != "" {
				t.Errorf("a refused path still returned %q", got)
			}
		})
	}
}

func TestResolveConfigPathAcceptsWhatIsInside(t *testing.T) {
	allowed := []string{"/var/lib/postgresql", "/etc/postgresql"}

	tests := []struct {
		name      string
		requested string
		want      string
	}{
		{
			name:      "a file in an allowed root",
			requested: "/var/lib/postgresql/data/pg_hba.conf",
			want:      "/var/lib/postgresql/data/pg_hba.conf",
		},
		{
			name:      "the root itself",
			requested: "/etc/postgresql",
			want:      "/etc/postgresql",
		},
		{
			name:      "redundant separators are cleaned",
			requested: "/var/lib/postgresql//data///pg_hba.conf",
			want:      "/var/lib/postgresql/data/pg_hba.conf",
		},
		{
			name:      "a dot segment is cleaned",
			requested: "/var/lib/postgresql/./data/pg_hba.conf",
			want:      "/var/lib/postgresql/data/pg_hba.conf",
		},
		{
			name:      "a climb that stays inside is allowed",
			requested: "/var/lib/postgresql/data/../data/pg_hba.conf",
			want:      "/var/lib/postgresql/data/pg_hba.conf",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveConfigPath(tc.requested, allowed)
			if err != nil {
				t.Fatalf("resolveConfigPath(%q) = %v, want %q", tc.requested, err, tc.want)
			}
			if got != tc.want {
				t.Errorf("resolveConfigPath(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

// An operator who sent a `..` cannot see where it landed from the path they
// typed, so the refusal has to report the resolved one.
func TestRefusalReportsTheResolvedPath(t *testing.T) {
	_, err := resolveConfigPath("/var/lib/postgresql/../../etc/shadow",
		[]string{"/var/lib/postgresql"})
	if err == nil {
		t.Fatal("a traversal was accepted")
	}
	if !strings.Contains(err.Error(), "/etc/shadow") {
		t.Errorf("the refusal does not say where the path resolved to: %v", err)
	}
}

// An empty entry in the allowlist must not become a root that matches
// everything: filepath.Clean("") is ".", and "." is a prefix of nothing useful,
// but a bug here would open the whole filesystem.
func TestEmptyAllowlistEntryIsNotAWildcard(t *testing.T) {
	for _, allowed := range [][]string{nil, {}, {""}, {"", ""}} {
		if _, err := resolveConfigPath("/etc/shadow", allowed); !errors.Is(err, ErrPathNotAllowed) {
			t.Errorf("allowlist %q admitted /etc/shadow: %v", allowed, err)
		}
	}
}

// The real allowlist comes from system.GetPostgresDataDirs(), which on this
// platform is a list of absolute directories. Guard against one of them being
// a relative path, which would make withinRoot compare against a cleaned ".".
func TestRootsAreComparedAsAbsolutePaths(t *testing.T) {
	got, err := resolveConfigPath("/var/lib/postgresql/data/pg_hba.conf",
		[]string{"/var/lib/postgresql/"})
	if err != nil {
		t.Fatalf("a trailing separator on the root broke the match: %v", err)
	}
	if got != filepath.Clean("/var/lib/postgresql/data/pg_hba.conf") {
		t.Errorf("got %q", got)
	}
}

package validator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrNoAuthRule reports a pg_hba.conf with no rule that can authenticate anyone.
//
// Writing one locks every client out of the database, including the agent that
// would be used to put it back, so recovery needs console access to the host.
// That is the failure this validator exists to prevent, and until 2026-09-14 it
// did not: the rule was `strings.Contains(content, "host")`, which a file of
// nothing but comments satisfies — "# no host entries here" contains "host".
var ErrNoAuthRule = errors.New("pg_hba.conf has no authentication rule")

// ErrMalformedRule reports a rule with too few fields to be understood.
//
// PostgreSQL refuses to load the whole file when one line is malformed, so a
// single bad rule is the same outage as an empty file.
var ErrMalformedRule = errors.New("malformed pg_hba.conf rule")

// Postgres validates a PostgreSQL configuration file before it is written to a
// database host.
type Postgres struct{}

// Validate reports content that must not be written.
//
// It answers only for pg_hba.conf. Anything else is not this validator's
// business and is passed, which is the caller's contract — management.UpdateConfig
// selects validators by matching the file name.
func (v *Postgres) Validate(_ context.Context, filePath string, content string) error {
	if filepath.Base(filePath) != "pg_hba.conf" {
		return nil
	}
	return validateHBA(content)
}

// validateHBA parses the file the way PostgreSQL does: comments stripped, blank
// lines ignored, and every surviving line a rule that must have enough fields
// to mean something.
func validateHBA(content string) error {
	var rules int

	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)

		// A `#` anywhere begins a comment, so this is what makes a file of
		// prose fail rather than pass on an accidental substring.
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line == "" {
			continue
		}

		// An `include` directive pulls in rules this validator cannot see, so
		// it counts as a rule rather than being judged on its field count.
		if fields := strings.Fields(line); isInclude(fields[0]) {
			rules++
			continue
		}

		if err := validateRule(line); err != nil {
			return fmt.Errorf("%w on line %d: %w", ErrMalformedRule, i+1, err)
		}
		rules++
	}

	if rules == 0 {
		return fmt.Errorf("%w: writing it would lock every client out of the "+
			"database, and the agent with them", ErrNoAuthRule)
	}
	return nil
}

func isInclude(keyword string) bool {
	switch strings.ToLower(keyword) {
	case "include", "include_if_exists", "include_dir":
		return true
	}
	return false
}

// validateRule checks one connection rule's shape.
//
// The field counts are PostgreSQL's: `local` takes DATABASE USER METHOD, every
// other type also takes an ADDRESS. Deliberately shape-only — this does not
// judge whether a method is a good idea, because refusing a method an operator
// legitimately needs is its own outage.
func validateRule(line string) error {
	fields := strings.Fields(line)
	connType := strings.ToLower(fields[0])

	var want int
	switch connType {
	case "local":
		want = 4
	case "host", "hostssl", "hostnossl", "hostgssenc", "hostnogssenc":
		want = 5
	default:
		return fmt.Errorf("%q is not a connection type", fields[0])
	}

	if len(fields) < want {
		return fmt.Errorf("%q needs at least %d fields, got %d: %q",
			connType, want, len(fields), line)
	}
	return nil
}

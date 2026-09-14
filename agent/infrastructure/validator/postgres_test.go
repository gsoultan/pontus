package validator_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/agent/infrastructure/validator"
)

func TestValidateAcceptsUsableFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "an ordinary file",
			content: `# TYPE  DATABASE  USER  ADDRESS        METHOD
local   all       all                  peer
host    all       all   127.0.0.1/32   scram-sha-256
host    all       all   ::1/128        scram-sha-256`,
		},
		{
			name:    "a single local rule is enough",
			content: "local all all trust",
		},
		{
			name:    "hostssl and its negations are connection types",
			content: "hostssl all all 0.0.0.0/0 cert\nhostnossl all all 0.0.0.0/0 reject",
		},
		{
			name:    "gss variants are connection types",
			content: "hostgssenc all all 0.0.0.0/0 gss\nhostnogssenc all all 0.0.0.0/0 reject",
		},
		{
			name:    "extra option fields are allowed",
			content: "host all all 0.0.0.0/0 ldap ldapserver=x ldapbasedn=y",
		},
		{
			name:    "an include pulls in rules this validator cannot see",
			content: "# nothing local\ninclude_if_exists /etc/postgresql/extra.conf",
		},
		{
			name:    "a trailing comment on a rule does not break it",
			content: "host all all 127.0.0.1/32 scram-sha-256 # the app",
		},
		{
			name:    "indentation is not significant",
			content: "\t  local   all   all   peer  \n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := (&validator.Postgres{}).Validate(context.Background(), "/etc/postgresql/16/main/pg_hba.conf", tc.content); err != nil {
				t.Fatalf("a usable pg_hba.conf was refused: %v", err)
			}
		})
	}
}

// The regression this file exists for. The old rule was
// `strings.Contains(content, "host") || strings.Contains(content, "local")`,
// so every one of these passed validation and would have been written to a
// production database host, locking out every client and the agent with them.
func TestValidateRefusesFilesThatLockEveryoneOut(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    error
	}{
		{
			name:    "empty",
			content: "",
			want:    validator.ErrNoAuthRule,
		},
		{
			name:    "only whitespace",
			content: "\n\n   \n\t\n",
			want:    validator.ErrNoAuthRule,
		},
		{
			name:    "only comments, mentioning host",
			content: "# no host entries here\n# and no local ones either\n",
			want:    validator.ErrNoAuthRule,
		},
		{
			name:    "a comment that would have satisfied the old substring rule",
			content: "# host all all 0.0.0.0/0 trust   <- commented out during the incident",
			want:    validator.ErrNoAuthRule,
		},
		{
			name:    "prose that happens to contain the word local",
			content: "this file is managed locally, do not edit",
			want:    validator.ErrMalformedRule,
		},
		{
			name:    "a truncated host rule",
			content: "host all all 127.0.0.1/32",
			want:    validator.ErrMalformedRule,
		},
		{
			name:    "a truncated local rule",
			content: "local all all",
			want:    validator.ErrMalformedRule,
		},
		{
			name:    "a good rule followed by a malformed one",
			content: "local all all peer\nhost all all",
			want:    validator.ErrMalformedRule,
		},
		{
			name:    "a connection type PostgreSQL does not know",
			content: "tcp all all 0.0.0.0/0 md5",
			want:    validator.ErrMalformedRule,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (&validator.Postgres{}).Validate(context.Background(), "/data/pg_hba.conf", tc.content)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate(%q) = %v, want %v", tc.content, err, tc.want)
			}
		})
	}
}

// A malformed rule stops PostgreSQL loading the whole file, so the operator
// needs the line number rather than "validation failed".
func TestMalformedRuleReportsTheLine(t *testing.T) {
	err := (&validator.Postgres{}).Validate(context.Background(), "/data/pg_hba.conf",
		"# header\nlocal all all peer\n\nhost all all\n")
	if !errors.Is(err, validator.ErrMalformedRule) {
		t.Fatalf("got %v, want ErrMalformedRule", err)
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("the refusal does not give the line number: %v", err)
	}
}

// The validator answers for pg_hba.conf and nothing else. It used to match on
// strings.Contains(filePath, "pg_hba.conf"), which caught a backup file and
// missed a file reached by a path it did not expect.
func TestValidateOnlyJudgesHBA(t *testing.T) {
	v := &validator.Postgres{}

	// Not pg_hba.conf: not this validator's business, whatever the content.
	for _, path := range []string{
		"/etc/postgresql/16/main/postgresql.conf",
		"/etc/postgresql/16/main/pg_ident.conf",
	} {
		if err := v.Validate(context.Background(), path, "anything at all"); err != nil {
			t.Errorf("Validate(%q) refused a file it does not judge: %v", path, err)
		}
	}

	// pg_hba.conf reached by any path is judged.
	for _, path := range []string{
		"pg_hba.conf",
		"/var/lib/postgresql/data/pg_hba.conf",
		"/etc/postgresql/16/main/pg_hba.conf",
	} {
		if err := v.Validate(context.Background(), path, ""); !errors.Is(err, validator.ErrNoAuthRule) {
			t.Errorf("Validate(%q) did not judge a pg_hba.conf: %v", path, err)
		}
	}
}

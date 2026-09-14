package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/pkg/config"
)

func TestValidateProtocol(t *testing.T) {
	tests := []struct {
		name         string
		protocol     string
		experimental bool
		want         error
	}{
		{name: "unset means postgres", protocol: ""},
		{name: "postgres", protocol: "postgres"},
		{name: "postgres is case insensitive", protocol: "PostgreSQL", want: config.ErrUnknownProtocol},
		{name: "postgres upper", protocol: "POSTGRES"},
		{name: "surrounding space is trimmed", protocol: "  postgres  "},

		{
			name:     "mysql without the opt-in is refused",
			protocol: "mysql",
			want:     config.ErrMySQLExperimental,
		},
		{
			name:         "mysql with the opt-in is served",
			protocol:     "mysql",
			experimental: true,
		},
		{
			name:         "the opt-in does not excuse a typo",
			protocol:     "mysqll",
			experimental: true,
			want:         config.ErrUnknownProtocol,
		},

		// The regression this file exists for: `default:` in the registry used
		// to serve PostgreSQL for anything it did not recognise, so a typo in
		// the field that frames every byte on the wire started a proxy that
		// looked healthy.
		{
			name:     "a near miss for postgres is refused, not guessed at",
			protocol: "postgre",
			want:     config.ErrUnknownProtocol,
		},
		{
			name:     "an unrelated protocol is refused",
			protocol: "mongodb",
			want:     config.ErrUnknownProtocol,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateProtocol(tc.protocol, tc.experimental)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("ValidateProtocol(%q, %v) = %v, want nil", tc.protocol, tc.experimental, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateProtocol(%q, %v) = %v, want %v", tc.protocol, tc.experimental, err, tc.want)
			}
		})
	}
}

// The refusal has to tell an operator which capability they are missing,
// otherwise the flag reads like a formality to switch on and move past.
func TestMySQLRefusalNamesTheGaps(t *testing.T) {
	err := config.ValidateProtocol("mysql", false)
	if err == nil {
		t.Fatal("mysql was accepted without experimental_mysql")
	}

	for _, want := range []string{
		"GetCurrentLSN", "WaitLSN", "ReplayPreparedStatements",
		"DiscoverTopology", "experimental_mysql",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// An unknown protocol must quote the offending value; "unknown protocol" alone
// sends an operator looking through a file for a value they cannot see.
func TestUnknownProtocolQuotesTheValue(t *testing.T) {
	err := config.ValidateProtocol("postgre", false)
	if err == nil {
		t.Fatal("a typo'd protocol was accepted")
	}
	if !strings.Contains(err.Error(), `"postgre"`) {
		t.Errorf("the refusal does not quote the value: %v", err)
	}
}

package config

import (
	"errors"
	"fmt"
	"strings"
)

// ProtocolPostgres and ProtocolMySQL are the wire protocols Pontus speaks.
const (
	ProtocolPostgres = "postgres"
	ProtocolMySQL    = "mysql"
)

// ErrUnknownProtocol reports a protocol Pontus has no handler for.
//
// Previously an unrecognised value fell through a `default:` to the PostgreSQL
// handler, so `protocol: postgre` started a healthy-looking proxy that would
// have answered a MySQL client with PostgreSQL bytes. A typo in the field that
// decides how every byte on the wire is framed is not a thing to guess at.
var ErrUnknownProtocol = errors.New("unknown protocol")

// ErrMySQLExperimental reports MySQL selected without the opt-in.
//
// The MySQL handler implements enough of the protocol to look like it works and
// not enough to be trusted with a session: GetCurrentLSN and WaitLSN return
// empty, so read-your-writes consistency silently does nothing;
// ReplayPreparedStatements returns nil, so a backend switch loses every prepared
// statement; DiscoverTopology returns nil, so replicas are never found; and
// CollectMetrics returns a zero struct, so the dashboard reports a healthy
// backend it has measured nothing about.
//
// Each of those is a silent wrong answer rather than an error, which is the
// worst shape a gap can take. Until they are implemented, selecting MySQL is a
// decision an operator makes explicitly.
var ErrMySQLExperimental = errors.New("mysql is experimental")

// ValidateProtocol reports a protocol that must not be served.
//
// Shared by startup validation and the registry, because a project can also
// arrive from the management store with a protocol nobody validated at boot.
func ValidateProtocol(protocol string, experimentalMySQL bool) error {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	// Unset means postgres, which is what internal/app defaults it to.
	case "", ProtocolPostgres:
		return nil

	case ProtocolMySQL:
		if experimentalMySQL {
			return nil
		}
		return fmt.Errorf("%w: GetCurrentLSN, WaitLSN, ReplayPreparedStatements "+
			"and DiscoverTopology are not implemented, so read-your-writes "+
			"consistency, prepared statements across a backend switch, and "+
			"replica discovery silently do nothing. Set experimental_mysql: true "+
			"to serve it anyway", ErrMySQLExperimental)

	default:
		return fmt.Errorf("%w: %q is not %q or %q", ErrUnknownProtocol,
			protocol, ProtocolPostgres, ProtocolMySQL)
	}
}

package protocol

import (
	"errors"
	"fmt"
)

// maxSlotNameLen is PostgreSQL's own limit on a replication slot name.
const maxSlotNameLen = 63

// ErrInvalidSlotName reports a slot name PostgreSQL would not accept.
var ErrInvalidSlotName = errors.New("invalid replication slot name")

// ErrInvalidOutputPlugin reports an output plugin name that is not a plain
// identifier.
var ErrInvalidOutputPlugin = errors.New("invalid output plugin name")

// ValidateSlotName checks a replication slot name against PostgreSQL's rules:
// lower-case letters, digits and underscores, at most 63 characters.
//
// This is a whitelist rather than an escape. Slot names reach us from the
// management API, and the statements that create them are assembled as text
// because the simple query protocol has no bind parameters — so a name that
// cannot be expressed safely must be rejected rather than quoted and hoped
// over.
func ValidateSlotName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidSlotName)
	}
	if len(name) > maxSlotNameLen {
		return fmt.Errorf("%w: longer than %d characters", ErrInvalidSlotName, maxSlotNameLen)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return fmt.Errorf("%w: %q is not allowed; use lower-case letters, digits and underscores",
				ErrInvalidSlotName, r)
		}
	}
	return nil
}

// ValidateOutputPlugin checks a logical decoding plugin name.
//
// Plugins are identifiers naming a shared library the server will load, so the
// same whitelist applies, with upper-case allowed since some plugins use it.
func ValidateOutputPlugin(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidOutputPlugin)
	}
	if len(name) > maxSlotNameLen {
		return fmt.Errorf("%w: longer than %d characters", ErrInvalidOutputPlugin, maxSlotNameLen)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return fmt.Errorf("%w: %q is not allowed", ErrInvalidOutputPlugin, r)
		}
	}
	return nil
}

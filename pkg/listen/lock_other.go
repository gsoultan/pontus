//go:build !unix

package listen

import (
	"errors"
	"os"
)

var errWouldBlock = errors.New("lock is held")

// Without flock there is nothing to coordinate with, so a single process takes
// the lock and an overlapping upgrade is not available on this platform —
// which is also true of reuse_port, so the two are unavailable together.
func lockExclusive(_ *os.File) error { return nil }

func unlock(_ *os.File) error { return nil }

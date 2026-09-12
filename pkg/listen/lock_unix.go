//go:build unix

package listen

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// errWouldBlock marks a lock another process already holds.
var errWouldBlock = errors.New("lock is held")

func lockExclusive(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errWouldBlock
	}
	return err
}

func unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

//go:build unix

package listen

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// controlReusePort sets SO_REUSEPORT on the socket before it is bound.
//
// SO_REUSEPORT rather than SO_REUSEADDR: the two are not interchangeable.
// REUSEADDR lets a socket bind an address in TIME_WAIT, which is about
// restarting quickly. REUSEPORT lets two *live* sockets share one address and
// have the kernel distribute connections between them, which is what carries
// traffic across an upgrade.
func controlReusePort(_, _ string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return setErr
}

//go:build !unix

package listen

import (
	"fmt"
	"syscall"
)

// controlReusePort refuses on platforms without SO_REUSEPORT.
//
// Windows has SO_REUSEADDR with entirely different semantics — it lets an
// unrelated process steal a bound port — so mapping the option onto it would
// turn a performance feature into a hijacking primitive. Refusing to start is
// the honest answer.
func controlReusePort(_, _ string, _ syscall.RawConn) error {
	return fmt.Errorf("reuse_port is not supported on this platform: SO_REUSEPORT " +
		"is a unix facility, and Windows' SO_REUSEADDR lets an unrelated process " +
		"take over the port rather than share it")
}

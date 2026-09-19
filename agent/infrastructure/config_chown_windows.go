//go:build windows

package infrastructure

import "os"

// chownLike is a no-op on Windows, which has no uid/gid to copy. The
// replacement file inherits the directory's ACL, which is the behaviour an
// operator on this platform expects.
func chownLike(_ string, _ os.FileInfo) error { return nil }

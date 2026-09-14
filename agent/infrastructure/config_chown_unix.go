//go:build !windows

package infrastructure

import (
	"fmt"
	"os"
	"syscall"
)

// chownLike gives the replacement file the owner of the file it replaces.
//
// The agent runs as root on a database host, so a file it creates is owned by
// root. PostgreSQL refuses to start when its data directory is not owned by the
// server account, and a pg_hba.conf it cannot read is an outage, so replacing a
// postgres-owned file with a root-owned one would break the cluster on its next
// restart — long after the change that caused it.
//
// A nil model means there was no previous file; the caller's directory already
// belongs to the right account and a new file inherits nothing, so this is left
// to the operator rather than guessed at.
func chownLike(path string, model os.FileInfo) error {
	if model == nil {
		return nil
	}

	stat, ok := model.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}

	if err := os.Chown(path, int(stat.Uid), int(stat.Gid)); err != nil {
		// Not fatal when the agent is not root: an unprivileged agent editing a
		// file it already owns cannot chown it and does not need to.
		if os.IsPermission(err) {
			return nil
		}
		return fmt.Errorf("cannot give %s the owner of the file it replaces: %w", path, err)
	}
	return nil
}

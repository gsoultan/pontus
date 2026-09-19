package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"
)

// backupSuffix marks the copy writeConfigFile leaves behind.
//
// A distinctive name rather than `.bak`: PostgreSQL reads nothing in its data
// directory by extension, but an operator looking at the directory after an
// incident needs to know which tool left the file and that it is the previous
// version rather than a hand-made copy.
const backupSuffix = ".pontus-prev"

// writeConfigFile replaces a PostgreSQL configuration file, atomically, keeping
// the previous contents beside it.
//
// Until 2026-09-14 this did not exist: UpdateConfig validated the content, then
// returned Success: true over a commented-out os.WriteFile. The dashboard,
// pontusctl and the ConnectRPC API all reported the edit had been applied.
//
// Atomic because the alternative is a truncated pg_hba.conf: os.WriteFile
// truncates before it writes, so a crash or a full disk between the two leaves
// a file PostgreSQL will not load, on a host whose database is now unreachable.
// A temp file in the same directory followed by rename is never observed
// half-written, and stays on the same filesystem so the rename cannot fail with
// EXDEV.
func writeConfigFile(path, content string) error {
	dir := filepath.Dir(path)

	// Mode and ownership are inherited from the file being replaced. PostgreSQL
	// refuses to start when its configuration is group- or world-readable, and
	// a new file created by a root-run agent would be owned by root, which the
	// server also rejects. When there is no existing file, 0600 is the mode
	// initdb uses.
	mode := os.FileMode(0o600)
	existing, statErr := os.Stat(path)
	if statErr == nil {
		mode = existing.Mode().Perm()

		if err := backupConfigFile(path, mode); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("cannot read %s before replacing it: %w", path, statErr)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".pontus-*")
	if err != nil {
		return fmt.Errorf("cannot create a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Any failure past this point leaves the original in place, so the temp
	// file is the only thing to clean up.
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write %s: %w", tmpName, err)
	}

	// Durability before visibility: the rename is atomic with respect to a
	// concurrent reader either way, but without the sync a power loss can leave
	// the rename applied and the contents empty.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot flush %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot close %s: %w", tmpName, err)
	}

	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("cannot set mode %v on %s: %w", mode, tmpName, err)
	}
	if err := chownLike(tmpName, existing); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}
	tmpName = ""

	return syncDir(dir)
}

// backupConfigFile copies the current contents aside before they are replaced.
//
// A copy rather than a rename: a rename would make the original briefly absent,
// and PostgreSQL reads pg_hba.conf on every connection.
func backupConfigFile(path string, mode os.FileMode) error {
	previous, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s before replacing it: %w", path, err)
	}
	if err := os.WriteFile(path+backupSuffix, previous, mode); err != nil {
		return fmt.Errorf("cannot save the previous %s: %w", path, err)
	}
	return nil
}

// syncDir makes the rename itself durable. Without it the new contents survive
// a power loss but the directory entry pointing at them may not.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("cannot open %s to flush it: %w", dir, err)
	}
	defer d.Close()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("cannot flush %s: %w", dir, err)
	}
	return nil
}

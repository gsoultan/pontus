package infrastructure

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrPathNotAllowed reports a file the agent will not touch.
var ErrPathNotAllowed = errors.New("path is not allowed")

// resolveConfigPath returns the cleaned absolute path a config write may target,
// or an error naming why it may not.
//
// The allowlist used to be `strings.HasPrefix(req.FilePath, p)` over the raw
// request field, which fails in both directions on a process running as root:
//
//   - `/var/lib/postgresql/../../etc/cron.d/pwn` has the allowed prefix and
//     resolves outside it, so the allowlist admitted arbitrary paths;
//   - `/var/lib/postgresql-backup/x` also has the prefix, so a sibling
//     directory that shares a name was inside the boundary by accident.
//
// Cleaning first closes the traversal; comparing whole path segments rather
// than a string prefix closes the sibling. The agent token authorises managing
// a database, not owning the host.
func resolveConfigPath(requested string, allowed []string) (string, error) {
	if requested == "" {
		return "", fmt.Errorf("%w: no path given", ErrPathNotAllowed)
	}
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("%w: %q is relative; an agent resolves it against "+
			"whatever directory it happens to be running in", ErrPathNotAllowed, requested)
	}

	clean := filepath.Clean(requested)

	for _, root := range allowed {
		if root == "" {
			continue
		}
		if withinRoot(clean, filepath.Clean(root)) {
			return clean, nil
		}
	}

	// The cleaned path is reported, not the requested one: an operator who sent
	// a `..` needs to see where it actually landed.
	return "", fmt.Errorf("%w: %q resolves to %q, which is outside every "+
		"configured PostgreSQL directory", ErrPathNotAllowed, requested, clean)
}

// withinRoot reports whether path is root or sits beneath it, comparing whole
// segments so that "/var/lib/postgresql-backup" is not inside
// "/var/lib/postgresql".
func withinRoot(path, root string) bool {
	if path == root {
		return true
	}
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(path, root)
}

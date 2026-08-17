package credentials

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
)

// FileStore reads verifiers from a static file, for deployments that will not
// grant the read auth_query needs.
//
// Format, one role per line, whitespace-separated, matching what pg_authid
// stores:
//
//	alice  SCRAM-SHA-256$4096:<salt>$<StoredKey>:<ServerKey>
//	bob    md5<32 hex characters>
//	carol                       # no password
//
// Blank lines and # comments are ignored. A quoted name may contain spaces:
//
//	"reporting user"  SCRAM-SHA-256$...
type FileStore struct {
	mu    sync.RWMutex
	users map[string]Verifier
	path  string
}

// NewFileStore loads the file once. Reload re-reads it.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload re-reads the file, replacing what is held only if the whole file
// parses. A half-applied credential file is worse than a stale one: it would
// lock out whichever roles happened to follow the bad line.
func (s *FileStore) Reload() error {
	file, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("auth file: %w", err)
	}
	defer func() { _ = file.Close() }()

	if err := checkPermissions(file); err != nil {
		return err
	}

	users := make(map[string]Verifier)
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		name, verifier, ok, err := parseLine(scanner.Text())
		if err != nil {
			return fmt.Errorf("auth file %s line %d: %w", s.path, line, err)
		}
		if ok {
			users[name] = verifier
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("auth file %s: %w", s.path, err)
	}

	s.mu.Lock()
	s.users = users
	s.mu.Unlock()
	return nil
}

// checkPermissions refuses a world-readable credential file.
//
// The file holds every role's verifier, which is enough to verify any client's
// proof. Leaving it readable by every account on the host is the sort of thing
// that is noticed only after it matters.
func checkPermissions(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("auth file: %w", err)
	}
	if mode := info.Mode().Perm(); mode&fs.FileMode(0o077) != 0 {
		return fmt.Errorf("auth file %s is mode %04o; it holds password verifiers "+
			"and must not be readable by group or other (chmod 600)", file.Name(), mode)
	}
	return nil
}

func parseLine(raw string) (string, Verifier, bool, error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", Verifier{}, false, nil
	}

	name, rest, err := splitName(line)
	if err != nil {
		return "", Verifier{}, false, err
	}

	verifier, err := ParseVerifier(strings.TrimSpace(rest))
	if err != nil {
		return "", Verifier{}, false, err
	}
	return name, verifier, true, nil
}

// splitName takes the role name, honouring double quotes so a name may contain
// spaces — as PostgreSQL role names may.
func splitName(line string) (name, rest string, err error) {
	if !strings.HasPrefix(line, `"`) {
		name, rest, _ = strings.Cut(line, " ")
		if tabbed, tabRest, ok := strings.Cut(name, "\t"); ok {
			name, rest = tabbed, tabRest+" "+rest
		}
		return name, rest, nil
	}

	closing := strings.Index(line[1:], `"`)
	if closing < 0 {
		return "", "", fmt.Errorf("unterminated quoted role name")
	}
	return line[1 : closing+1], line[closing+2:], nil
}

// Lookup implements Store.
func (s *FileStore) Lookup(_ context.Context, user string) (Verifier, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	verifier, ok := s.users[user]
	if !ok {
		return Verifier{}, fmt.Errorf("%w: %q", ErrUnknownUser, user)
	}
	return verifier, nil
}

// Len reports how many roles are loaded.
func (s *FileStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

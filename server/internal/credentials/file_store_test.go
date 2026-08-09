package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAuthFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "userlist.txt")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
	return path
}

func TestFileStoreLoadsRoles(t *testing.T) {
	path := writeAuthFile(t, `
# comment line
alice  `+scramText("4096")+`
bob    md5`+strings.Repeat("b", 32)+`
carol
"reporting user"  md5`+strings.Repeat("c", 32)+`
`, 0o600)

	s, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if s.Len() != 4 {
		t.Fatalf("loaded %d roles, want 4", s.Len())
	}

	alice, err := s.Lookup(t.Context(), "alice")
	if err != nil || alice.Method != MethodSCRAM {
		t.Errorf("alice = %v, %v", alice.Method, err)
	}
	// A role with no password is loaded, not skipped: Pontus must know it exists
	// and that it cannot authenticate as it.
	carol, err := s.Lookup(t.Context(), "carol")
	if err != nil || carol.Method != MethodNone {
		t.Errorf("carol = %v, %v", carol.Method, err)
	}
	if _, err := s.Lookup(t.Context(), "reporting user"); err != nil {
		t.Errorf("a quoted role name with a space was not loaded: %v", err)
	}
	if _, err := s.Lookup(t.Context(), "nobody"); !errors.Is(err, ErrUnknownUser) {
		t.Errorf("err = %v, want ErrUnknownUser", err)
	}
}

// The file holds every role's verifier — enough to verify any client's proof.
// Leaving it readable by every account on the host is noticed only after it
// matters.
func TestFileStoreRefusesWorldReadableFile(t *testing.T) {
	path := writeAuthFile(t, "alice md5"+strings.Repeat("a", 32)+"\n", 0o644)

	_, err := NewFileStore(path)
	if err == nil {
		t.Fatal("loaded a world-readable credential file")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("error does not say how to fix it: %v", err)
	}
}

// A half-applied credential file would lock out whichever roles followed the
// bad line, which is worse than a stale one.
func TestFileStoreRejectsTheWholeFileOnABadLine(t *testing.T) {
	good := writeAuthFile(t, "alice md5"+strings.Repeat("a", 32)+"\n", 0o600)
	s, err := NewFileStore(good)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(good,
		[]byte("alice md5"+strings.Repeat("a", 32)+"\nbob not-a-verifier\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err == nil {
		t.Fatal("reloaded a file with an unparseable line")
	}
	// The previous contents must still be usable.
	if _, err := s.Lookup(t.Context(), "alice"); err != nil {
		t.Errorf("a failed reload discarded the working credentials: %v", err)
	}
}

func TestFileStoreMissingFile(t *testing.T) {
	if _, err := NewFileStore(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing auth file was accepted")
	}
}

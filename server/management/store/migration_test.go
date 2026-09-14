package store_test

import (
	"path/filepath"
	"testing"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/server/management/store"
)

// Schema setup runs on every start, so "applied twice" is the normal case, not
// an edge one. AGENTS.md asks for it proven on a fresh *and* a populated
// database — a CREATE TABLE IF NOT EXISTS is trivially idempotent while empty
// and the interesting question is whether the second run preserves rows.
//
// There is no versioned migration system here: schema setup is
// CREATE TABLE IF NOT EXISTS plus one ad-hoc ALTER TABLE whose error is
// discarded. That happens to be idempotent today. This test is what will notice
// when the next migration is not.
func TestSchemaSetupIsIdempotentOnAPopulatedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "management.db")

	db, err := store.NewManagementDB(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	projects := store.NewSQLiteProject(db)
	users := store.NewSQLiteUser(db)
	settings := store.NewSQLiteSetting(db)

	if err := projects.Upsert(&domain.Project{Id: "p1", Name: "prod", Protocol: "postgres"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := users.Upsert("alice", "s3cret", "admin"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := settings.Set(t.Context(), "balancer", "least_conns"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Second start against the same file: every store's init runs again.
	reopened, err := store.NewManagementDB(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })

	projects2 := store.NewSQLiteProject(reopened)
	users2 := store.NewSQLiteUser(reopened)
	settings2 := store.NewSQLiteSetting(reopened)

	if got := projects2.List(); len(got) != 1 || got[0].Id != "p1" {
		t.Errorf("projects after the second start: %#v, want the one seeded", got)
	}

	found := false
	for _, u := range users2.List() {
		if u.Username == "alice" {
			found = true
			if u.Role != "admin" {
				t.Errorf("alice's role is %q, want admin", u.Role)
			}
		}
	}
	if !found {
		t.Error("the seeded user did not survive the second start")
	}

	value, err := settings2.Get(t.Context(), "balancer")
	if err != nil {
		t.Fatalf("read setting: %v", err)
	}
	if value != "least_conns" {
		t.Errorf("setting is %q, want least_conns", value)
	}
}

// A third and fourth start must be no different from the second. The ad-hoc
// `ALTER TABLE users RENAME COLUMN password TO password_hash` is made
// idempotent by discarding its error, which works only because the failure it
// discards is always "no such column" — a real error would be discarded too.
func TestSchemaSetupSurvivesRepeatedStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "management.db")

	for i := range 4 {
		db, err := store.NewManagementDB(path)
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}

		users := store.NewSQLiteUser(db)
		if i == 0 {
			if err := users.Upsert("bob", "hunter2", "viewer"); err != nil {
				t.Fatalf("seed on start %d: %v", i+1, err)
			}
		}

		if got := len(users.List()); got != 1 {
			t.Fatalf("start %d sees %d users, want 1", i+1, got)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close %d: %v", i+1, err)
		}
	}
}

// A stored password is a bcrypt hash, not the plaintext handed to Upsert.
// Included here because a schema change that silently reverted the
// password/password_hash rename would show up as a readable password rather
// than as a failure.
func TestStoredPasswordIsNotThePlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "management.db")
	db, err := store.NewManagementDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	users := store.NewSQLiteUser(db)
	if err := users.Upsert("carol", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatal(err)
	}

	for _, u := range users.List() {
		if u.Username != "carol" {
			continue
		}
		if u.Token == "correct-horse-battery-staple" {
			t.Fatal("the password is stored in plaintext")
		}
		if len(u.Token) == 0 {
			t.Fatal("no password hash was stored")
		}
	}
}

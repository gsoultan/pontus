package store

import (
	"os"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/pkg/auth"
)

func TestSQLiteUserStore_Upsert(t *testing.T) {
	db, err := NewManagementDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to open management db: %v", err)
	}
	defer db.Close()

	s := NewSQLiteUser(db)

	username := "testuser"
	password := "plainpassword"
	role := "admin"

	// Test 1: Storing raw password
	if err := s.Upsert(username, password, role); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	u, ok := s.Get(username)
	if !ok {
		t.Fatalf("User not found")
	}

	if u.Token == password {
		t.Errorf("Password was stored in plain text")
	}

	if !strings.HasPrefix(u.Token, "$2a$") && !strings.HasPrefix(u.Token, "$2b$") && !strings.HasPrefix(u.Token, "$2y$") {
		t.Errorf("Stored password does not look like a bcrypt hash: %s", u.Token)
	}

	if !auth.CheckPasswordHash(password, u.Token) {
		t.Errorf("Password hash verification failed")
	}

	// Test 2: Storing already hashed password (should not double-hash)
	hashed, _ := auth.HashPassword("newpassword")
	if err := s.Upsert(username, hashed, role); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	u, ok = s.Get(username)
	if !ok {
		t.Fatalf("User not found")
	}

	if u.Token != hashed {
		t.Errorf("Hashed password was double-hashed or modified")
	}
}

func TestJSONUserStore_Upsert(t *testing.T) {
	filePath := "test_users.json"
	s, err := NewJSONUserStore(filePath)
	if err != nil {
		t.Fatalf("Failed to create json user store: %v", err)
	}
	// No defer os.Remove(filePath) here because we want to make sure it saves, but actually better to cleanup

	username := "jsonuser"
	password := "jsonpassword"
	role := "viewer"

	// Test 1: Storing raw password
	if err := s.Upsert(username, password, role); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	u, ok := s.Get(username)
	if !ok {
		t.Fatalf("User not found")
	}

	if u.Token == password {
		t.Errorf("Password was stored in plain text")
	}

	if !auth.CheckPasswordHash(password, u.Token) {
		t.Errorf("Password hash verification failed")
	}

	// Test 2: Storing already hashed password
	hashed, _ := auth.HashPassword("anotherpass")
	if err := s.Upsert(username, hashed, role); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	u, ok = s.Get(username)
	if !ok {
		t.Fatalf("User not found")
	}

	if u.Token != hashed {
		t.Errorf("Hashed password was double-hashed or modified")
	}

	// Cleanup
	_ = os.Remove(filePath)
}

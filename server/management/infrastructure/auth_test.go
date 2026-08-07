package infrastructure

import (
	"github.com/gsoultan/pontus/pkg/auth"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/management/store"
)

func TestAuth_Login(t *testing.T) {
	db, err := store.NewManagementDB(":memory:")
	if err != nil {
		t.Fatalf("failed to open management db: %v", err)
	}
	defer db.Close()

	pstore := store.NewSQLiteProject(db)
	ustore := store.NewSQLiteUser(db)
	sstore := store.NewSQLiteSetting(db)

	issuer, err := auth.NewIssuer("test-secret")
	if err != nil {
		t.Fatalf("failed to create issuer: %v", err)
	}

	svc := NewService(t.Context(), pstore, ustore, sstore, 1*time.Second, nil, issuer, nil)

	// 1. Create a user
	username := "testuser"
	password := "testpassword"
	_, err = svc.CreateUser(t.Context(), &endpoints.CreateUserRequest{
		Username: username,
		Password: password,
		Role:     "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 2. Try to login
	resp, err := svc.Login(t.Context(), &endpoints.LoginRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	if resp.Username != username {
		t.Errorf("expected username %s, got %s", username, resp.Username)
	}
	if resp.Token == "" {
		t.Error("expected token, got empty string")
	}

	// Verify that password in store is hashed
	u, ok := ustore.Get(username)
	if !ok {
		t.Fatal("user not found in store")
	}
	if u.Token == password {
		t.Error("password in store is not hashed")
	}

	// 3. Try login with wrong password
	_, err = svc.Login(t.Context(), &endpoints.LoginRequest{
		Username: username,
		Password: "wrongpassword",
	})
	if err == nil {
		t.Error("expected error for wrong password, got nil")
	}
}

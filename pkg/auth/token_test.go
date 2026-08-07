package auth

import (
	"errors"
	"strings"
	"testing"
)

// NewAuth used to fall back to the literal secret "pontus-secret-key" when
// none was configured, so anyone reading the repository could mint an
// administrator token against a default deployment.
func TestNewIssuerFailsClosedWithoutKey(t *testing.T) {
	issuer, err := NewIssuer("")
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("expected ErrNoKey, got %v", err)
	}
	if issuer != nil {
		t.Error("expected no issuer when the key is unset")
	}
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	issuer, err := NewIssuer("a-configured-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	token, err := issuer.Issue("operator", "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Username != "operator" || claims.Role != "admin" {
		t.Errorf("claims = %+v, want operator/admin", claims)
	}
}

// PASETO v4.local is encrypted, not merely signed: the role must not be
// readable from the token body the way a JWT's base64 claims are.
func TestTokenDoesNotLeakClaims(t *testing.T) {
	issuer, err := NewIssuer("a-configured-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	token, err := issuer.Issue("operator", "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if !strings.HasPrefix(token, "v4.local.") {
		t.Errorf("token = %q, want a v4.local token", token)
	}
	if strings.Contains(token, "operator") || strings.Contains(token, "admin") {
		t.Error("token body exposes its claims in cleartext")
	}
}

func TestVerifyRejectsForeignKey(t *testing.T) {
	mine, err := NewIssuer("my-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	theirs, err := NewIssuer("their-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	token, err := theirs.Issue("mallory", "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := mine.Verify(token); err == nil {
		t.Error("a token minted under a different key was accepted")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	issuer, err := NewIssuer("a-configured-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	for _, token := range []string{
		"",
		"not-a-token",
		"v4.local.",
		// A JWT must not be accepted now that tokens are PASETO.
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoiYWRtaW4ifQ.x",
		// `alg: none` — the JWT confusion this migration removes.
		"eyJhbGciOiJub25lIn0.eyJyb2xlIjoiYWRtaW4ifQ.",
	} {
		if _, err := issuer.Verify(token); err == nil {
			t.Errorf("Verify(%q) succeeded, want rejection", token)
		}
	}
}

func TestIssueRequiresUsername(t *testing.T) {
	issuer, err := NewIssuer("a-configured-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	if _, err := issuer.Issue("", "admin"); err == nil {
		t.Error("expected an error for an empty username")
	}
}

// A long passphrase must not be truncated to 32 bytes, and two different long
// passphrases must produce different keys.
func TestKeyDerivationUsesFullSecret(t *testing.T) {
	prefix := strings.Repeat("x", 64)
	a, err := NewIssuer(prefix + "-one")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	b, err := NewIssuer(prefix + "-two")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	token, err := a.Issue("operator", "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := b.Verify(token); err == nil {
		t.Error("secrets sharing a 32-byte prefix produced the same key")
	}
}

//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// A token the server no longer accepts must be rejected as Unauthenticated,
// not as some other failure.
//
// This is what the dashboard keys its recovery off. A stored token that the
// server will not accept — after the switch from JWT to PASETO, or after the
// signing key is rotated — left the UI permanently broken: isAuthenticated is
// persisted in the browser, so the router admitted the operator and every
// request then failed with "invalid token" and no route back to signing in.
//
// The browser can only recover from that if the rejection is distinguishable
// from an ordinary error, so assert the code rather than the message.
func TestStaleTokenIsRejectedAsUnauthenticated(t *testing.T) {
	s := startStack(t)
	projectID, proxyID := "", ""

	// A well-formed token from another issuer: what a browser holds after the
	// token format or signing key has changed underneath it.
	stale := "v4.local.OtherIssuerPayloadThatCannotVerifyAgainstThisKey"

	out, code := s.rpc("GetStatus",
		map[string]string{"projectId": projectID, "proxyId": proxyID}, stale)

	if code == http.StatusOK {
		t.Fatal("a token from another issuer was accepted")
	}

	// Connect maps Unauthenticated to 401. Anything else and the dashboard
	// cannot tell "sign in again" from "something went wrong".
	if code != http.StatusUnauthorized {
		t.Errorf("stale token produced HTTP %d, want 401 so the UI can end the session: %v", code, out)
	}
	if message := strings.ToLower(strings.TrimSpace(fmtAny(out["message"]))); message != "" {
		if !strings.Contains(message, "token") && !strings.Contains(message, "unauthenticated") {
			t.Errorf("rejection did not identify itself as a credentials problem: %q", message)
		}
	}
}

// A legacy JWT must be refused the same way, since that is exactly what an
// existing browser is holding after the migration.
func TestLegacyJWTIsRejectedAsUnauthenticated(t *testing.T) {
	s := startStack(t)

	// Shape of an HS256 JWT; the server no longer speaks this.
	legacy := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJ1c2VybmFtZSI6ImFkbWluIiwicm9sZSI6ImFkbWluIn0.notavalidsignature"

	_, code := s.rpc("ListProjects", map[string]string{}, legacy)
	if code == http.StatusOK {
		t.Fatal("a legacy JWT was accepted after the migration to PASETO")
	}
	if code != http.StatusUnauthorized {
		t.Errorf("legacy JWT produced HTTP %d, want 401", code)
	}
}

// After signing in again the session must work, which is the other half of the
// recovery the dashboard performs.
func TestFreshLoginRecoversAfterRejection(t *testing.T) {
	s := startStack(t)

	if _, code := s.rpc("ListProjects", map[string]string{}, "v4.local.garbage"); code == http.StatusOK {
		t.Fatal("a garbage token was accepted")
	}

	token := s.login()
	if _, code := s.rpc("ListProjects", map[string]string{}, token); code != http.StatusOK {
		t.Errorf("signing in again did not restore access (%d)", code)
	}
}

func fmtAny(v any) string {
	s, _ := v.(string)
	return s
}

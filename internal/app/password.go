package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// bootstrapPasswordBytes is the entropy behind a generated admin password.
// 24 bytes is 192 bits, well beyond anything worth brute-forcing, and encodes
// to a 32-character string an operator can still copy by hand.
const bootstrapPasswordBytes = 24

// generatePassword returns a URL-safe random password for the bootstrap
// administrator. It fails rather than falling back to anything predictable —
// a weak password here is a public administrator account.
func generatePassword() (string, error) {
	buf := make([]byte, bootstrapPasswordBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

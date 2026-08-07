// Package auth issues and verifies the management API's session tokens.
package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	paseto "aidanwoods.dev/go-paseto"
)

// TokenTTL is how long an issued session token stays valid.
const TokenTTL = 24 * time.Hour

// Issuer mints and verifies PASETO v4.local tokens.
//
// v4.local is symmetric and *encrypted* (XChaCha20-Poly1305), not merely
// signed. The management API is both issuer and verifier, so there is no key
// distribution problem to justify an asymmetric scheme, and encrypting means
// the username and role are not readable by anything holding the token.
//
// This replaces HS256 JWT. PASETO fixes the class of failure JWT keeps
// producing: there is no `alg` header to confuse, so a token cannot be
// downgraded to `none` or tricked into being verified with the wrong
// primitive. The version and purpose are baked into the token's first bytes.
type Issuer struct {
	key paseto.V4SymmetricKey
}

// ErrNoKey reports that no signing key was configured.
//
// There is deliberately no default. A build-time fallback secret means every
// deployment that forgets to set one shares a key that is public in the source
// tree, and anyone who reads the repo can mint an administrator token.
var ErrNoKey = errors.New("auth: no token key configured")

// NewIssuer derives an issuer from the configured secret.
//
// The secret is hashed to the 32 bytes PASETO requires, so an operator may
// supply a passphrase of any length without it being silently truncated or
// padded. Callers must treat ErrNoKey as fatal at startup.
func NewIssuer(secret string) (*Issuer, error) {
	if secret == "" {
		return nil, ErrNoKey
	}

	digest := sha256.Sum256([]byte(secret))
	key, err := paseto.V4SymmetricKeyFromBytes(digest[:])
	if err != nil {
		return nil, fmt.Errorf("auth: derive token key: %w", err)
	}
	return &Issuer{key: key}, nil
}

// Claims is the identity carried by a session token.
type Claims struct {
	Username string
	Role     string
}

// Issue mints a token for the given identity.
func (i *Issuer) Issue(username, role string) (string, error) {
	if username == "" {
		return "", errors.New("auth: username is required")
	}

	now := time.Now()
	token := paseto.NewToken()
	token.SetIssuedAt(now)
	token.SetNotBefore(now)
	token.SetExpiration(now.Add(TokenTTL))
	token.SetIssuer("pontus")
	token.SetString("username", username)
	token.SetString("role", role)

	return token.V4Encrypt(i.key, nil), nil
}

// Verify decrypts a token and returns its claims.
//
// The parser is constructed with the standard temporal rules, so an expired or
// not-yet-valid token is rejected here rather than by the caller remembering
// to check.
func (i *Issuer) Verify(token string) (Claims, error) {
	parser := paseto.NewParser()
	parser.AddRule(paseto.NotExpired())
	parser.AddRule(paseto.ValidAt(time.Now()))
	parser.AddRule(paseto.IssuedBy("pontus"))

	parsed, err := parser.ParseV4Local(i.key, token, nil)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: invalid token: %w", err)
	}

	username, err := parsed.GetString("username")
	if err != nil {
		return Claims{}, errors.New("auth: token has no username")
	}
	role, err := parsed.GetString("role")
	if err != nil {
		// A token without a role is not assumed to be an administrator.
		role = ""
	}

	return Claims{Username: username, Role: role}, nil
}

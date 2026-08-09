package credentials

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Method is how a role's password is stored, which decides what Pontus can do
// with it.
type Method string

const (
	// MethodSCRAM is PostgreSQL's default since 14.
	MethodSCRAM Method = "scram-sha-256"

	// MethodMD5 is the pre-14 default. The stored value is
	// md5(password‖username), which is exactly what an md5 auth exchange with a
	// backend needs — so unlike SCRAM it is directly usable.
	MethodMD5 Method = "md5"

	// MethodNone is a role with no password. Such a role can only connect where
	// pg_hba says trust or peer.
	MethodNone Method = "none"
)

var (
	// ErrUnknownUser is returned when a role has no entry.
	ErrUnknownUser = errors.New("no credential for user")

	// ErrUnsupportedVerifier is returned for a stored format Pontus cannot use.
	ErrUnsupportedVerifier = errors.New("unsupported password verifier")
)

// Verifier is a role's stored password verifier, as it appears in
// pg_authid.rolpassword.
//
// It is deliberately not a password. A SCRAM verifier cannot authenticate to a
// server on its own — see SCRAM below — and an md5 verifier can. Keeping the
// distinction in the type stops a caller assuming the wrong one.
type Verifier struct {
	Method Method

	// MD5 is the stored "md5<hex>" value without its prefix, for MethodMD5.
	MD5 string

	// SCRAM holds the parsed parts, for MethodSCRAM.
	SCRAM *SCRAMVerifier
}

// SCRAMVerifier is the parsed form of
// SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey>.
//
// StoredKey is SHA256(ClientKey), so this alone **cannot** authenticate to a
// backend: the hash cannot be reversed. What it can do is verify a client's
// proof, and in doing so recover ClientKey — which is what makes SCRAM
// pass-through possible without ever storing a plaintext password. See
// docs/design/backend-auth.md.
type SCRAMVerifier struct {
	Iterations int
	Salt       []byte
	StoredKey  []byte
	ServerKey  []byte
}

// ParseVerifier reads a pg_authid.rolpassword value.
//
// An empty value is a role with no password, which is a legitimate answer
// rather than an error: it means the role can only arrive over a trust or peer
// line in pg_hba, and Pontus must not pretend it can authenticate as it.
func ParseVerifier(stored string) (Verifier, error) {
	stored = strings.TrimSpace(stored)

	switch {
	case stored == "":
		return Verifier{Method: MethodNone}, nil

	case strings.HasPrefix(stored, "SCRAM-SHA-256$"):
		scram, err := parseSCRAM(strings.TrimPrefix(stored, "SCRAM-SHA-256$"))
		if err != nil {
			return Verifier{}, err
		}
		return Verifier{Method: MethodSCRAM, SCRAM: scram}, nil

	case strings.HasPrefix(stored, "md5") && len(stored) == 35:
		// "md5" plus 32 hex characters. The length check matters: a plaintext
		// password beginning "md5" would otherwise be read as a hash.
		hash := stored[3:]
		if !isHex(hash) {
			return Verifier{}, fmt.Errorf("%w: md5 value is not hexadecimal", ErrUnsupportedVerifier)
		}
		return Verifier{Method: MethodMD5, MD5: hash}, nil

	default:
		// Anything else — a plaintext password stored by an old server, or a
		// format from a future one — is refused rather than guessed at. Guessing
		// here would mean authenticating with something that is not a credential.
		return Verifier{}, fmt.Errorf("%w: unrecognised format", ErrUnsupportedVerifier)
	}
}

func parseSCRAM(body string) (*SCRAMVerifier, error) {
	// <iterations>:<salt>$<StoredKey>:<ServerKey>
	saltPart, keyPart, ok := strings.Cut(body, "$")
	if !ok {
		return nil, fmt.Errorf("%w: SCRAM verifier has no key section", ErrUnsupportedVerifier)
	}

	iterText, saltB64, ok := strings.Cut(saltPart, ":")
	if !ok {
		return nil, fmt.Errorf("%w: SCRAM verifier has no salt", ErrUnsupportedVerifier)
	}

	iterations, err := strconv.Atoi(iterText)
	if err != nil || iterations <= 0 {
		return nil, fmt.Errorf("%w: SCRAM iteration count %q", ErrUnsupportedVerifier, iterText)
	}

	storedB64, serverB64, ok := strings.Cut(keyPart, ":")
	if !ok {
		return nil, fmt.Errorf("%w: SCRAM verifier has no server key", ErrUnsupportedVerifier)
	}

	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return nil, fmt.Errorf("%w: SCRAM salt is not base64", ErrUnsupportedVerifier)
	}
	storedKey, err := base64.StdEncoding.DecodeString(storedB64)
	if err != nil {
		return nil, fmt.Errorf("%w: SCRAM stored key is not base64", ErrUnsupportedVerifier)
	}
	serverKey, err := base64.StdEncoding.DecodeString(serverB64)
	if err != nil {
		return nil, fmt.Errorf("%w: SCRAM server key is not base64", ErrUnsupportedVerifier)
	}

	// SHA-256 keys are 32 bytes. A verifier of any other length is not one this
	// code can use, and accepting it would fail later inside the exchange where
	// the cause is far less obvious.
	if len(storedKey) != 32 || len(serverKey) != 32 {
		return nil, fmt.Errorf("%w: SCRAM keys are %d/%d bytes, want 32",
			ErrUnsupportedVerifier, len(storedKey), len(serverKey))
	}

	return &SCRAMVerifier{
		Iterations: iterations,
		Salt:       salt,
		StoredKey:  storedKey,
		ServerKey:  serverKey,
	}, nil
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// String deliberately hides the material.
//
// A Verifier reaches logs by accident — in an error, a struct dump, a %v on a
// wrapping type — and the SCRAM keys are enough to verify a client's proof and
// recover its ClientKey, which is password-equivalent. There is no reason to
// ever print one.
func (v Verifier) String() string {
	return "credentials.Verifier(" + string(v.Method) + ", redacted)"
}

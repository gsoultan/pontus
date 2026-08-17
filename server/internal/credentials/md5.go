package credentials

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// PostgreSQL's md5 authentication.
//
// The stored verifier is `md5` + hex(md5(password‖username)), and the exchange
// is one round: the server sends four salt bytes, the client answers
// `md5` + hex(md5(storedHex‖salt)).
//
// Unlike SCRAM, the stored verifier is *sufficient* in both directions — it is
// exactly what the client proves knowledge of. So Pontus can verify a client
// and authenticate to a backend from the same value, with no key recovery and
// nothing password-equivalent beyond the verifier itself.
//
// It is also weak: md5, unsalted at rest, and the stored value is a password
// equivalent for anyone who obtains it. PostgreSQL has defaulted to SCRAM since
// 14 and deprecates this. Supported because refusing it strands every pre-14
// deployment, not because it is a good idea.

// MD5Salt is the four-byte challenge.
type MD5Salt [4]byte

// NewMD5Salt returns a fresh challenge.
//
// It must be unpredictable: the response is a hash over the stored verifier and
// this salt, so a reused salt makes a captured response replayable.
func NewMD5Salt() (MD5Salt, error) {
	var salt MD5Salt
	if _, err := rand.Read(salt[:]); err != nil {
		return salt, fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}
	return salt, nil
}

// MD5Response computes what a client holding this verifier must send.
//
// Used in both directions: to check a client's answer, and to answer a
// backend's challenge on that client's behalf.
func MD5Response(storedHex string, salt MD5Salt) string {
	sum := md5.Sum(append([]byte(storedHex), salt[:]...))
	return "md5" + hex.EncodeToString(sum[:])
}

// VerifyMD5 checks a client's response against the stored verifier.
//
// Constant-time: a byte-wise comparison leaks how much of the response was
// correct, and the response is a deterministic function of the verifier and a
// salt the attacker was just handed.
func VerifyMD5(verifier Verifier, salt MD5Salt, response string) error {
	if verifier.Method != MethodMD5 {
		return fmt.Errorf("%w: not an md5 verifier", ErrAuthFailed)
	}

	want := MD5Response(verifier.MD5, salt)
	if subtle.ConstantTimeCompare([]byte(response), []byte(want)) != 1 {
		return ErrAuthFailed
	}
	return nil
}

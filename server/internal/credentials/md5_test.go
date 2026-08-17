package credentials

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// storedFor derives the verifier PostgreSQL keeps for a role: the md5 of the
// password concatenated with the user name.
func storedFor(password, user string) string {
	sum := md5.Sum([]byte(password + user))
	return hex.EncodeToString(sum[:])
}

// The property that makes md5 usable in both directions: the stored verifier is
// exactly what the client proves knowledge of, so the same value verifies a
// client and answers a backend. SCRAM needs key recovery for this; md5 does not.
func TestMD5ResponseIsTheSameBothDirections(t *testing.T) {
	stored := storedFor("hunter2", "alice")
	salt := MD5Salt{1, 2, 3, 4}

	clientAnswer := MD5Response(stored, salt)
	pontusAnswerToABackend := MD5Response(stored, salt)

	if clientAnswer != pontusAnswerToABackend {
		t.Error("the response Pontus checks and the one it sends differ; " +
			"an md5 credential would not be reusable toward a backend")
	}
	if !strings.HasPrefix(clientAnswer, "md5") {
		t.Errorf("response %q lacks the md5 prefix PostgreSQL expects", clientAnswer)
	}
	if len(clientAnswer) != 35 {
		t.Errorf("response is %d characters, want 35", len(clientAnswer))
	}
}

func TestVerifyMD5AcceptsTheRightAnswer(t *testing.T) {
	verifier := Verifier{Method: MethodMD5, MD5: storedFor("hunter2", "alice")}
	salt := MD5Salt{9, 8, 7, 6}

	if err := VerifyMD5(verifier, salt, MD5Response(verifier.MD5, salt)); err != nil {
		t.Fatalf("a correct response was rejected: %v", err)
	}
}

func TestVerifyMD5RejectsAWrongAnswer(t *testing.T) {
	verifier := Verifier{Method: MethodMD5, MD5: storedFor("hunter2", "alice")}
	salt := MD5Salt{9, 8, 7, 6}

	wrong := MD5Response(storedFor("not-the-password", "alice"), salt)
	if err := VerifyMD5(verifier, salt, wrong); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// A response computed against a different salt must not be accepted, or a
// captured one could be replayed.
func TestVerifyMD5RejectsAResponseForAnotherSalt(t *testing.T) {
	verifier := Verifier{Method: MethodMD5, MD5: storedFor("hunter2", "alice")}

	captured := MD5Response(verifier.MD5, MDSalt(1))
	if err := VerifyMD5(verifier, MDSalt(2), captured); !errors.Is(err, ErrAuthFailed) {
		t.Fatal("a response for a different salt was accepted; it would be replayable")
	}
}

func MDSalt(b byte) MD5Salt { return MD5Salt{b, b, b, b} }

// A SCRAM verifier must not be used for an md5 exchange: the values are not
// interchangeable and accepting one would authenticate against the wrong thing.
func TestVerifyMD5RefusesANonMD5Verifier(t *testing.T) {
	verifier := Verifier{Method: MethodSCRAM}
	if err := VerifyMD5(verifier, MDSalt(1), "md5whatever"); err == nil {
		t.Error("a SCRAM verifier was accepted for an md5 exchange")
	}
}

// The salt is what stops a response being replayable, so it has to be fresh.
func TestMD5SaltIsUnpredictable(t *testing.T) {
	seen := map[MD5Salt]bool{}
	for range 50 {
		salt, err := NewMD5Salt()
		if err != nil {
			t.Fatal(err)
		}
		if seen[salt] {
			t.Fatalf("salt %v was issued twice", salt)
		}
		seen[salt] = true
	}
}

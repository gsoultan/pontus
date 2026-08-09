package credentials

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// synthetic keys: 32 bytes each, obviously fake, so no real verifier is
// committed to the repository.
func fakeKey(fill byte) string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = fill
	}
	return base64.StdEncoding.EncodeToString(b)
}

func scramText(iter string) string {
	salt := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	return "SCRAM-SHA-256$" + iter + ":" + salt + "$" + fakeKey(0x11) + ":" + fakeKey(0x22)
}

func TestParseSCRAMVerifier(t *testing.T) {
	v, err := ParseVerifier(scramText("4096"))
	if err != nil {
		t.Fatalf("ParseVerifier: %v", err)
	}
	if v.Method != MethodSCRAM {
		t.Fatalf("method = %q, want scram-sha-256", v.Method)
	}
	if v.SCRAM.Iterations != 4096 {
		t.Errorf("iterations = %d, want 4096", v.SCRAM.Iterations)
	}
	if len(v.SCRAM.StoredKey) != 32 || len(v.SCRAM.ServerKey) != 32 {
		t.Errorf("keys are %d/%d bytes, want 32", len(v.SCRAM.StoredKey), len(v.SCRAM.ServerKey))
	}
	if string(v.SCRAM.Salt) != "0123456789abcdef" {
		t.Errorf("salt = %q", v.SCRAM.Salt)
	}
}

func TestParseMD5Verifier(t *testing.T) {
	v, err := ParseVerifier("md5" + strings.Repeat("a", 32))
	if err != nil {
		t.Fatalf("ParseVerifier: %v", err)
	}
	if v.Method != MethodMD5 {
		t.Fatalf("method = %q, want md5", v.Method)
	}
	if v.MD5 != strings.Repeat("a", 32) {
		t.Errorf("md5 payload = %q", v.MD5)
	}
}

// A role with no password is an answer, not an error: it can only arrive over a
// trust or peer line, and Pontus must not pretend it can authenticate as it.
func TestParseEmptyVerifierIsNoPassword(t *testing.T) {
	v, err := ParseVerifier("")
	if err != nil {
		t.Fatalf("ParseVerifier: %v", err)
	}
	if v.Method != MethodNone {
		t.Errorf("method = %q, want none", v.Method)
	}
}

func TestParseVerifierRejectsMalformed(t *testing.T) {
	for name, stored := range map[string]string{
		"plaintext":        "hunter2",
		"md5 wrong length": "md5abc",
		"md5 not hex":      "md5" + strings.Repeat("z", 32),
		"scram no keys":    "SCRAM-SHA-256$4096:c2FsdA==",
		"scram no salt":    "SCRAM-SHA-256$4096$" + fakeKey(1) + ":" + fakeKey(2),
		"scram bad iter":   scramText("not-a-number"),
		"scram zero iter":  scramText("0"),
		"scram short keys": "SCRAM-SHA-256$4096:c2FsdA==$c2hvcnQ=:c2hvcnQ=",
		"scram bad base64": "SCRAM-SHA-256$4096:!!!!$" + fakeKey(1) + ":" + fakeKey(2),
		"unknown scheme":   "ARGON2$whatever",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseVerifier(stored); err == nil {
				t.Errorf("accepted %q; guessing at a credential format means "+
					"authenticating with something that is not one", stored)
			}
		})
	}
}

// A plaintext password beginning "md5" must not be mistaken for a hash.
func TestParseVerifierDoesNotMistakeAPasswordForAnMD5Hash(t *testing.T) {
	v, err := ParseVerifier("md5isnotmypasswordhonestly")
	if err == nil {
		t.Fatalf("accepted a plaintext password as a verifier: %+v", v)
	}
	if !errors.Is(err, ErrUnsupportedVerifier) {
		t.Errorf("err = %v, want ErrUnsupportedVerifier", err)
	}
}

// A Verifier reaches logs by accident — inside an error, a struct dump, a %v on
// a wrapper. The SCRAM keys verify a client's proof and yield its ClientKey,
// which is password-equivalent, so printing one is never right.
func TestVerifierDoesNotPrintItsMaterial(t *testing.T) {
	v, err := ParseVerifier(scramText("4096"))
	if err != nil {
		t.Fatal(err)
	}

	printed := v.String()
	if strings.Contains(printed, fakeKey(0x11)) || strings.Contains(printed, fakeKey(0x22)) {
		t.Errorf("String() leaked key material: %q", printed)
	}
	if !strings.Contains(printed, "redacted") {
		t.Errorf("String() = %q, want it to say it is redacted", printed)
	}
}

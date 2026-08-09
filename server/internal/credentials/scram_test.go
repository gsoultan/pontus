package credentials

import (
	"bytes"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// makeVerifier derives a verifier the way PostgreSQL does when a password is
// set, so the tests below run against real material rather than fixtures.
func makeVerifier(t *testing.T, password string, iterations int) (*SCRAMVerifier, []byte) {
	t.Helper()

	salt := []byte("pontus-test-salt")
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}

	clientKey := hmacSHA256(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := hmacSHA256(salted, "Server Key")

	return &SCRAMVerifier{
		Iterations: iterations,
		Salt:       salt,
		StoredKey:  storedKey[:],
		ServerKey:  serverKey[:],
	}, clientKey
}

// scramClient is the client half, implemented in the test so the exchange is
// verified against an independent computation rather than against itself.
type scramClient struct {
	user      string
	password  string
	nonce     string
	firstBare string
}

func (c *scramClient) first() string {
	c.firstBare = "n=" + c.user + ",r=" + c.nonce
	return "n,," + c.firstBare
}

// final produces the client-final message and the ClientKey it used, so a test
// can assert the server recovered the same one.
func (c *scramClient) final(t *testing.T, serverFirst string) (msg string, clientKey []byte) {
	t.Helper()

	nonce, err := attr(serverFirst, 'r')
	if err != nil {
		t.Fatalf("server-first has no nonce: %v", err)
	}
	saltB64, err := attr(serverFirst, 's')
	if err != nil {
		t.Fatalf("server-first has no salt: %v", err)
	}
	iterText, err := attr(serverFirst, 'i')
	if err != nil {
		t.Fatalf("server-first has no iteration count: %v", err)
	}

	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		t.Fatalf("salt: %v", err)
	}

	iterations := 0
	for _, r := range iterText {
		iterations = iterations*10 + int(r-'0')
	}

	salted, err := pbkdf2.Key(sha256.New, c.password, salt, iterations, sha256.Size)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}
	clientKey = hmacSHA256(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)

	withoutProof := "c=biws,r=" + nonce
	authMessage := c.firstBare + "," + serverFirst + "," + withoutProof
	signature := hmacSHA256(storedKey[:], authMessage)
	proof := xorBytes(clientKey, signature)

	return withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof), clientKey
}

// The claim the whole backend-auth design rests on: verifying a client's proof
// also recovers its ClientKey, which is what Pontus needs to authenticate to a
// backend as that user. A stored verifier alone cannot do that, because
// StoredKey is SHA256(ClientKey) and a hash does not run backwards.
func TestScramRecoversTheClientKeyFromAValidProof(t *testing.T) {
	verifier, expectedClientKey := makeVerifier(t, "correct horse battery staple", 4096)

	server, err := NewScramServer(verifier)
	if err != nil {
		t.Fatal(err)
	}
	client := &scramClient{user: "alice", password: "correct horse battery staple", nonce: "clientnonce123"}

	serverFirst, err := server.Begin(client.first())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	clientFinal, clientKey := client.final(t, serverFirst)
	serverFinal, err := server.Finish(clientFinal)
	if err != nil {
		t.Fatalf("Finish rejected a correct proof: %v", err)
	}

	if !bytes.Equal(server.ClientKey(), expectedClientKey) {
		t.Error("the recovered ClientKey does not match the one derived from the password; " +
			"Pontus could not authenticate to a backend with it")
	}
	if !bytes.Equal(server.ClientKey(), clientKey) {
		t.Error("the recovered ClientKey does not match the one the client used")
	}

	// The client must also be able to authenticate the server, or it cannot tell
	// Pontus from something impersonating it.
	authMessage := client.firstBare + "," + serverFirst + "," +
		strings.TrimSuffix(clientFinal[:strings.LastIndex(clientFinal, ",p=")], "")
	want := "v=" + base64.StdEncoding.EncodeToString(hmacSHA256(verifier.ServerKey, authMessage))
	if serverFinal != want {
		t.Error("the server signature would not verify on the client side")
	}
}

func TestScramRejectsAWrongPassword(t *testing.T) {
	verifier, _ := makeVerifier(t, "the real password", 4096)

	server, _ := NewScramServer(verifier)
	client := &scramClient{user: "alice", password: "not the real password", nonce: "clientnonce123"}

	serverFirst, err := server.Begin(client.first())
	if err != nil {
		t.Fatal(err)
	}
	clientFinal, _ := client.final(t, serverFirst)

	if _, err := server.Finish(clientFinal); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	if server.ClientKey() != nil {
		t.Error("a ClientKey was retained after a failed exchange")
	}
}

// Channel binding ties the exchange to the client's TLS channel, which
// terminates at Pontus. Pontus cannot reproduce that binding toward a backend,
// so accepting 'p' would strip exactly the protection the client asked for.
func TestScramRefusesChannelBinding(t *testing.T) {
	verifier, _ := makeVerifier(t, "pw", 4096)
	server, _ := NewScramServer(verifier)

	_, err := server.Begin("p=tls-server-end-point,,n=alice,r=nonce")
	if !errors.Is(err, ErrChannelBindingUnsupported) {
		t.Fatalf("err = %v, want ErrChannelBindingUnsupported", err)
	}
}

// A client that opened with 'y' or 'n' must say the same in its final message.
// A different value between the two is a downgrade attempt.
func TestScramRefusesABindingAssertedLate(t *testing.T) {
	verifier, _ := makeVerifier(t, "pw", 4096)
	server, _ := NewScramServer(verifier)

	serverFirst, err := server.Begin("n,,n=alice,r=clientnonce")
	if err != nil {
		t.Fatal(err)
	}
	nonce, _ := attr(serverFirst, 'r')

	// c= is base64("p=tls-server-end-point,,"), which the client never announced.
	forged := "c=cD10bHMtc2VydmVyLWVuZC1wb2ludCws,r=" + nonce +
		",p=" + base64.StdEncoding.EncodeToString(make([]byte, 32))

	if _, err := server.Finish(forged); !errors.Is(err, ErrChannelBindingUnsupported) {
		t.Fatalf("err = %v, want ErrChannelBindingUnsupported", err)
	}
}

// A proof from a different exchange must not be accepted, which is what a
// replay looks like.
func TestScramRejectsAMismatchedNonce(t *testing.T) {
	verifier, _ := makeVerifier(t, "pw", 4096)
	server, _ := NewScramServer(verifier)

	if _, err := server.Begin("n,,n=alice,r=clientnonce"); err != nil {
		t.Fatal(err)
	}

	stale := "c=biws,r=someoneelsesnonce,p=" +
		base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := server.Finish(stale); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// The server nonce must be unpredictable, and must extend the client's.
func TestScramNonceIsFreshAndExtendsTheClients(t *testing.T) {
	verifier, _ := makeVerifier(t, "pw", 4096)

	seen := map[string]bool{}
	for range 20 {
		server, _ := NewScramServer(verifier)
		serverFirst, err := server.Begin("n,,n=alice,r=clientnonce")
		if err != nil {
			t.Fatal(err)
		}
		nonce, _ := attr(serverFirst, 'r')

		if !strings.HasPrefix(nonce, "clientnonce") {
			t.Fatalf("combined nonce %q does not start with the client's", nonce)
		}
		if nonce == "clientnonce" {
			t.Fatal("the server contributed no nonce of its own")
		}
		if seen[nonce] {
			t.Fatalf("nonce %q was issued twice", nonce)
		}
		seen[nonce] = true
	}
}

func TestScramRejectsMalformedMessages(t *testing.T) {
	verifier, _ := makeVerifier(t, "pw", 4096)

	for name, first := range map[string]string{
		"no gs2 header": "n=alice,r=nonce",
		"no nonce":      "n,,n=alice",
		"empty nonce":   "n,,n=alice,r=",
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := NewScramServer(verifier)
			if _, err := server.Begin(first); err == nil {
				t.Errorf("accepted %q", first)
			}
		})
	}

	t.Run("finish before begin", func(t *testing.T) {
		server, _ := NewScramServer(verifier)
		if _, err := server.Finish("c=biws,r=x,p=y"); err == nil {
			t.Error("Finish succeeded before Begin")
		}
	})

	t.Run("proof is not 32 bytes", func(t *testing.T) {
		server, _ := NewScramServer(verifier)
		serverFirst, _ := server.Begin("n,,n=alice,r=clientnonce")
		nonce, _ := attr(serverFirst, 'r')
		short := "c=biws,r=" + nonce + ",p=" + base64.StdEncoding.EncodeToString([]byte("short"))
		if _, err := server.Finish(short); err == nil {
			t.Error("accepted a proof of the wrong length")
		}
	})
}

func TestNewScramServerNeedsAVerifier(t *testing.T) {
	if _, err := NewScramServer(nil); err == nil {
		t.Error("built a SCRAM server with no verifier")
	}
}

// Independent check of the identity the recovery relies on, stated directly:
// ClientProof XOR ClientSignature == ClientKey.
func TestClientKeyRecoveryIdentity(t *testing.T) {
	_, clientKey := makeVerifier(t, "pw", 4096)
	storedKey := sha256.Sum256(clientKey)

	const authMessage = "n=alice,r=abc,r=abcdef,s=c2FsdA==,i=4096,c=biws,r=abcdef"
	signature := hmac.New(sha256.New, storedKey[:])
	signature.Write([]byte(authMessage))
	clientSignature := signature.Sum(nil)

	proof := xorBytes(clientKey, clientSignature)
	recovered := xorBytes(proof, clientSignature)

	if !bytes.Equal(recovered, clientKey) {
		t.Error("XOR recovery does not return the ClientKey; the design's premise is wrong")
	}
}

//go:build e2e

package protocol

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/credentials"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func backendAddr() string {
	if v := os.Getenv("PONTUS_E2E_BACKEND"); v != "" {
		return v
	}
	return "127.0.0.1:55832"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// The claim the whole design rests on, tested end to end against a real server:
// Pontus can open a PostgreSQL connection as a user holding only the ClientKey
// recovered from that user's own SCRAM proof — no password, ever.
//
// Everything before this was arithmetic that agreed with itself. This is the
// server deciding whether the proof is good.
func TestOpenBackendConnectionWithOnlyAClientKey(t *testing.T) {
	addr := backendAddr()
	if probe, err := net.DialTimeout("tcp", addr, 2*time.Second); err != nil {
		t.Skipf("no PostgreSQL at %s: %v", addr, err)
	} else {
		probe.Close()
	}

	const role = "pontus_key_probe"
	const password = "probe-password-for-key-auth"

	admin, err := sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		envOr("PONTUS_E2E_USER", "postgres"), envOr("PONTUS_E2E_PASSWORD", "postgres"),
		addr, envOr("PONTUS_E2E_DB", "postgres")))
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := admin.ExecContext(ctx, `DROP ROLE IF EXISTS `+role); err != nil {
		t.Fatalf("drop role: %v", err)
	}
	if _, err := admin.ExecContext(ctx,
		`CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(c, `DROP ROLE IF EXISTS `+role)
	})

	// Read the verifier the way Pontus would, via auth_query.
	store, err := credentials.NewQueryStore(credentials.SQLQuerier{DB: admin}, "")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := store.Lookup(ctx, role)
	if err != nil {
		t.Fatalf("auth_query: %v", err)
	}
	if verifier.Method != credentials.MethodSCRAM {
		t.Skipf("server stores %s verifiers", verifier.Method)
	}

	// Recover a ClientKey the way Pontus would: by verifying a client's proof.
	// The password is used *only* here, standing in for the client that knows
	// it. Nothing after this line sees it.
	clientKey := recoverClientKeyFromAClientProof(t, verifier.SCRAM, role, password)

	// Now authenticate to the real server with the key alone.
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatalf("dial backend: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	if err := AuthenticateBackend(conn, role, envOr("PONTUS_E2E_DB", "postgres"),
		clientKey, verifier.SCRAM, nil); err != nil {
		t.Fatalf("authenticating to a real PostgreSQL with only a ClientKey: %v", err)
	}

	startup, err := WaitForReady(conn)
	if err != nil {
		t.Fatalf("waiting for ReadyForQuery: %v", err)
	}
	if startup.Params["server_version"] == "" {
		t.Error("no server_version was reported; the startup did not complete")
	}
	if len(startup.BackendKey) == 0 {
		t.Error("no BackendKeyData was captured; a client that models the startup " +
			"sequence strictly treats its absence as a protocol error")
	}
	t.Logf("authenticated as %q with a ClientKey alone; server_version=%s",
		role, startup.Params["server_version"])
}

// A wrong key must be refused by the server, not merely by our own arithmetic.
func TestBackendRefusesAWrongClientKey(t *testing.T) {
	addr := backendAddr()
	if probe, err := net.DialTimeout("tcp", addr, 2*time.Second); err != nil {
		t.Skipf("no PostgreSQL at %s: %v", addr, err)
	} else {
		probe.Close()
	}

	admin, err := sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		envOr("PONTUS_E2E_USER", "postgres"), envOr("PONTUS_E2E_PASSWORD", "postgres"),
		addr, envOr("PONTUS_E2E_DB", "postgres")))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store, _ := credentials.NewQueryStore(credentials.SQLQuerier{DB: admin}, "")
	user := envOr("PONTUS_E2E_USER", "postgres")
	verifier, err := store.Lookup(ctx, user)
	if err != nil || verifier.Method != credentials.MethodSCRAM {
		t.Skipf("no SCRAM verifier to test against: %v", err)
	}

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	wrongKey := make([]byte, sha256.Size)
	for i := range wrongKey {
		wrongKey[i] = 0x5a
	}

	if err := AuthenticateBackend(conn, user, envOr("PONTUS_E2E_DB", "postgres"),
		wrongKey, verifier.SCRAM, nil); err == nil {
		t.Fatal("a fabricated ClientKey was accepted by a real PostgreSQL")
	}
}

// recoverClientKeyFromAClientProof runs the client half against Pontus's server
// half, exactly as a real login would, and returns what the server recovered.
func recoverClientKeyFromAClientProof(t *testing.T, v *credentials.SCRAMVerifier,
	user, password string) []byte {
	t.Helper()

	server, err := credentials.NewScramServer(v)
	if err != nil {
		t.Fatal(err)
	}

	firstBare := "n=" + user + ",r=clientnoncevalue"
	serverFirst, err := server.Begin("n,," + firstBare)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	clientFinal := buildClientFinal(t, password, firstBare, serverFirst)
	if _, err := server.Finish(clientFinal); err != nil {
		t.Fatalf("the client proof was rejected: %v", err)
	}

	key := server.ClientKey()
	if len(key) != sha256.Size {
		t.Fatalf("recovered a %d-byte key", len(key))
	}
	return key
}

func buildClientFinal(t *testing.T, password, firstBare, serverFirst string) string {
	t.Helper()

	fields := map[byte]string{}
	for _, field := range splitComma(serverFirst) {
		if len(field) > 2 && field[1] == '=' {
			fields[field[0]] = field[2:]
		}
	}

	salt, err := base64.StdEncoding.DecodeString(fields['s'])
	if err != nil {
		t.Fatalf("salt: %v", err)
	}
	iterations := 0
	for _, r := range fields['i'] {
		iterations = iterations*10 + int(r-'0')
	}

	salted := pbkdf2Key(t, password, salt, iterations)
	clientKey := hmac256(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)

	withoutProof := "c=biws,r=" + fields['r']
	authMessage := firstBare + "," + serverFirst + "," + withoutProof
	signature := hmac256(storedKey[:], authMessage)

	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ signature[i]
	}
	return withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof)
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func pbkdf2Key(t *testing.T, password string, salt []byte, iterations int) []byte {
	t.Helper()
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}
	return key
}

func hmac256(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

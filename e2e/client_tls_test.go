//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// selfSignedCert writes a certificate and key for 127.0.0.1.
func selfSignedCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "server.crt")
	keyPath = filepath.Join(dir, "server.key")

	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return certPath, keyPath
}

func writePEM(t *testing.T, path, kind string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// A client asking for TLS must get it.
//
// The `tls:` block was parsed and reached nothing, so Pontus answered every
// SSLRequest with "no" and each session ran in the clear — including the
// password exchange for any method other than SCRAM. PostgreSQL negotiates
// encryption inside the protocol, so this could never have worked by wrapping
// the listener; the handler has to answer the request and upgrade in place.
func TestClientTLSIsOffered(t *testing.T) {
	requireBackend(t)

	certPath, keyPath := selfSignedCert(t)

	s := startStackWith(t, func(cfg string) string {
		return cfg + fmt.Sprintf(`
tls:
  cert_file: %q
  key_file: %q
`, certPath, keyPath)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// sslmode=require: the client refuses to continue without encryption, so a
	// success here means TLS actually happened rather than a silent fallback.
	dsn := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=require",
		backendUser(), backendPass(), s.proxyAddr, backendDB())

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.TLSConfig.InsecureSkipVerify = true // self-signed, on purpose
	cfg.TLSConfig.ServerName = "localhost"

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("a client requiring TLS could not connect: %v\n"+
			"the tls block is configured, so an SSLRequest must be accepted", err)
	}
	defer conn.Close(context.Background())

	var n int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("query over TLS: %v", err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 returned %d", n)
	}

	// More than one statement, because the whole session now reads and writes
	// through the upgraded connection rather than the socket it opened with.
	for i := range 5 {
		if err := conn.QueryRow(ctx, fmt.Sprintf("SELECT %d", i)).Scan(&n); err != nil {
			t.Fatalf("statement %d over TLS: %v", i, err)
		}
	}
}

// With no certificate configured, an encryption request is declined and a
// client on sslmode=prefer carries on unencrypted — the behaviour every
// existing deployment has.
func TestWithoutTLSConfiguredClientsStillConnect(t *testing.T) {
	requireBackend(t)
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	conn := connectSimple(t, ctx, s)
	defer conn.Close(context.Background())

	var n int
	if err := conn.QueryRow(ctx, "SELECT 3").Scan(&n); err != nil || n != 3 {
		t.Fatalf("plaintext session broke: %v (n=%d)", err, n)
	}
}

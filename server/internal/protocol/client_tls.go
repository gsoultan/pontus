package protocol

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// Client-facing TLS.
//
// PostgreSQL does not do TLS at the transport layer: a client opens in
// plaintext, sends an eight-byte SSLRequest, and the server answers with a
// single byte — 'S' to proceed with a handshake, 'N' to carry on unencrypted.
// So this cannot be a tls.NewListener around the accept loop; the negotiation
// has to happen inside the protocol.
//
// The `tls:` config block was parsed and never reached the proxy listener
// (finding A3), which meant Pontus always answered 'N' and every client
// connection was in the clear — including the password exchange, when
// authentication is anything other than SCRAM.

// clientTLS is the configuration offered to clients, or nil for plaintext.
//
// Package-level and atomic for the same reason as the balancer's threshold: it
// is read on every connection and changes only on reload, and a plain variable
// would race with one.
var clientTLS atomic.Pointer[tls.Config]

// SetClientTLS installs the certificate Pontus presents to clients. Nil means
// encryption requests are declined and sessions continue in plaintext.
func SetClientTLS(cfg *tls.Config) {
	if cfg == nil {
		clientTLS.Store(nil)
		return
	}
	clientTLS.Store(cfg)
}

// ClientTLS returns the configuration, or nil.
func ClientTLS() *tls.Config { return clientTLS.Load() }

// negotiateTLS answers a client's SSLRequest.
//
// Declining is a legitimate answer — a client on sslmode=prefer falls back and
// continues — so a deployment with no certificate keeps working exactly as
// before. What must not happen is answering 'S' and then failing the handshake,
// which leaves the client waiting on a connection that will never speak.
func (p *PostgresHandler) negotiateTLS(client net.Conn) (net.Conn, error) {
	cfg := ClientTLS()
	if cfg == nil {
		if _, err := client.Write([]byte{'N'}); err != nil {
			return nil, fmt.Errorf("failed to decline encryption request: %w", err)
		}
		return client, nil
	}

	if _, err := client.Write([]byte{'S'}); err != nil {
		return nil, fmt.Errorf("failed to accept encryption request: %w", err)
	}

	// Bounded, because an unauthenticated client controls how long this takes.
	// A bare Handshake() waits forever, so a peer that opens a socket, asks for
	// TLS and then says nothing holds a goroutine indefinitely at no cost to
	// itself.
	ctx, cancel := context.WithTimeout(context.Background(), tlsHandshakeTimeout)
	defer cancel()

	server := tls.Server(client, cfg)
	if err := server.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("client TLS handshake: %w", err)
	}
	return server, nil
}

// tlsHandshakeTimeout bounds a client's TLS handshake. Generous for a slow
// network, finite because the peer is unauthenticated.
const tlsHandshakeTimeout = 15 * time.Second

package credentials

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// SCRAM-SHA-256, server side (RFC 5802 / RFC 7677).
//
// Pontus plays the server toward the client. What makes that worth doing —
// rather than relaying the client's exchange to a backend as before — is that
// verifying a proof also *recovers* the client's ClientKey:
//
//	ClientSignature = HMAC(StoredKey, AuthMessage)
//	ClientProof     = ClientKey XOR ClientSignature
//	  ⇒ ClientKey   = ClientProof XOR HMAC(StoredKey, AuthMessage)
//
// StoredKey comes from the stored verifier and AuthMessage is on the wire, so
// the XOR yields ClientKey — which is what a client needs to authenticate to a
// *backend*. That is how Pontus can open a connection as this user later
// without ever holding a plaintext password.
//
// ClientKey is therefore password-equivalent for the session that produced it:
// never log it, never persist it, and drop it when the session ends.

var (
	// ErrAuthFailed is returned for any failed exchange. Deliberately one error
	// for every cause: telling a caller whether the user existed, or how far the
	// proof got, is an oracle.
	ErrAuthFailed = errors.New("authentication failed")

	// ErrChannelBindingUnsupported is returned when a client insists on channel
	// binding.
	ErrChannelBindingUnsupported = errors.New("channel binding is not supported")

	// ErrProtocol marks a malformed exchange.
	ErrProtocol = errors.New("malformed SCRAM message")
)

// nonceBytes is the server nonce length. RFC 5802 requires only that it be
// unpredictable; 18 bytes is 24 base64 characters, matching what PostgreSQL
// itself generates.
const nonceBytes = 18

// ScramServer runs one SCRAM-SHA-256 exchange with one client.
//
// Not reusable: it holds the nonce and the transcript of a single exchange.
type ScramServer struct {
	verifier *SCRAMVerifier

	clientFirstBare string
	serverFirst     string
	nonce           string

	// clientKey is recovered from a valid proof. Password-equivalent.
	clientKey []byte
}

// NewScramServer starts an exchange against a stored verifier.
func NewScramServer(verifier *SCRAMVerifier) (*ScramServer, error) {
	if verifier == nil {
		return nil, fmt.Errorf("%w: no SCRAM verifier", ErrAuthFailed)
	}
	return &ScramServer{verifier: verifier}, nil
}

// Begin consumes the client-first message and returns the server-first message.
//
// The client-first message looks like `n,,n=alice,r=<nonce>`: a GS2 header, then
// the bare portion that becomes part of the transcript.
func (s *ScramServer) Begin(clientFirst string) (string, error) {
	gs2, bare, err := splitGS2(clientFirst)
	if err != nil {
		return "", err
	}

	// The GS2 flag is 'n' (client does not support channel binding), 'y' (client
	// supports it but believes the server does not), or 'p=<type>' (client
	// requires it).
	//
	// 'p' cannot be honoured and must not be waved through. Channel binding ties
	// the exchange to the client's TLS channel, which terminates at Pontus;
	// Pontus cannot reproduce that binding toward a backend, and pretending to
	// support it would strip exactly the protection the client asked for.
	if strings.HasPrefix(gs2, "p=") {
		return "", ErrChannelBindingUnsupported
	}

	clientNonce, err := attr(bare, 'r')
	if err != nil {
		return "", err
	}
	if clientNonce == "" {
		return "", fmt.Errorf("%w: empty client nonce", ErrProtocol)
	}

	serverNonce, err := newNonce()
	if err != nil {
		return "", err
	}

	s.clientFirstBare = bare
	s.nonce = clientNonce + serverNonce
	s.serverFirst = fmt.Sprintf("r=%s,s=%s,i=%d",
		s.nonce,
		base64.StdEncoding.EncodeToString(s.verifier.Salt),
		s.verifier.Iterations)

	return s.serverFirst, nil
}

// Finish verifies the client-final message and returns the server-final message.
//
// On success the client's ClientKey has been recovered and is available from
// ClientKey().
func (s *ScramServer) Finish(clientFinal string) (string, error) {
	if s.serverFirst == "" {
		return "", fmt.Errorf("%w: Finish before Begin", ErrProtocol)
	}

	withoutProof, proofB64, err := cutProof(clientFinal)
	if err != nil {
		return "", err
	}

	// The nonce must be the one this exchange issued, or the transcript belongs
	// to a different exchange — which is what a replay looks like.
	gotNonce, err := attr(withoutProof, 'r')
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare([]byte(gotNonce), []byte(s.nonce)) != 1 {
		return "", fmt.Errorf("%w: nonce does not match this exchange", ErrAuthFailed)
	}

	// A client that sent 'y' in its GS2 header believed the server did not
	// support channel binding. Its channel-binding attribute must say the same,
	// or a downgrade has been attempted between the two messages.
	binding, err := attr(withoutProof, 'c')
	if err != nil {
		return "", err
	}
	if err := checkBinding(binding); err != nil {
		return "", err
	}

	proof, err := base64.StdEncoding.DecodeString(proofB64)
	if err != nil || len(proof) != sha256.Size {
		return "", fmt.Errorf("%w: client proof is not %d bytes", ErrProtocol, sha256.Size)
	}

	authMessage := s.clientFirstBare + "," + s.serverFirst + "," + withoutProof

	// Recover ClientKey and check it against the stored key. This is both the
	// verification and the reason for doing the exchange here at all.
	clientSignature := hmacSHA256(s.verifier.StoredKey, authMessage)
	clientKey := xorBytes(proof, clientSignature)

	derived := sha256.Sum256(clientKey)
	if subtle.ConstantTimeCompare(derived[:], s.verifier.StoredKey) != 1 {
		return "", ErrAuthFailed
	}

	s.clientKey = clientKey

	serverSignature := hmacSHA256(s.verifier.ServerKey, authMessage)
	return "v=" + base64.StdEncoding.EncodeToString(serverSignature), nil
}

// ClientKey returns the recovered key, or nil if the exchange has not
// succeeded.
//
// Password-equivalent for this user: it is exactly what is needed to
// authenticate to a backend as them. Hold it for the session and no longer.
func (s *ScramServer) ClientKey() []byte { return s.clientKey }

// Nonce returns the combined nonce, for tests and diagnostics.
func (s *ScramServer) Nonce() string { return s.nonce }

// checkBinding accepts only the "no channel binding" GS2 headers.
//
// The base64 of "n,," is "biws" and of "y,," is "eSws". Anything else means the
// client is asserting a binding Pontus cannot have honoured.
func checkBinding(binding string) error {
	switch binding {
	case "biws", "eSws":
		return nil
	case "":
		return fmt.Errorf("%w: no channel-binding attribute", ErrProtocol)
	default:
		return ErrChannelBindingUnsupported
	}
}

// splitGS2 separates the GS2 header from the bare client-first message.
func splitGS2(clientFirst string) (gs2Flag, bare string, err error) {
	// <flag>,<authzid>,<bare>
	parts := strings.SplitN(clientFirst, ",", 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf("%w: client-first has no GS2 header", ErrProtocol)
	}
	return parts[0], parts[2], nil
}

// cutProof splits a client-final message at its proof attribute.
func cutProof(clientFinal string) (withoutProof, proof string, err error) {
	idx := strings.LastIndex(clientFinal, ",p=")
	if idx < 0 {
		return "", "", fmt.Errorf("%w: client-final has no proof", ErrProtocol)
	}
	return clientFinal[:idx], clientFinal[idx+3:], nil
}

// attr reads a single-letter SCRAM attribute.
func attr(message string, key byte) (string, error) {
	for field := range strings.SplitSeq(message, ",") {
		if len(field) >= 2 && field[0] == key && field[1] == '=' {
			return field[2:], nil
		}
	}
	return "", fmt.Errorf("%w: no %q attribute", ErrProtocol, string(key))
}

func hmacSHA256(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func newNonce() (string, error) {
	raw := make([]byte, nonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("%w: %w", ErrAuthFailed, err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

package credentials

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
)

// ScramClient is the client half of SCRAM-SHA-256, used when Pontus opens a
// connection to a backend on a user's behalf.
//
// It is built from a **ClientKey**, not a password. That is the whole point:
// ScramServer recovers ClientKey while verifying a client's proof, so Pontus
// can then prove knowledge to a backend without ever having seen the password.
//
// ClientKey is password-equivalent for that user. Build one per backend
// connection, use it, and let it go with the session.
type ScramClient struct {
	user      string
	clientKey []byte
	storedKey []byte

	nonce     string
	firstBare string
	authMsg   string
}

// NewScramClient prepares an exchange using a recovered ClientKey.
//
// StoredKey is derived rather than passed: it is SHA256(ClientKey) by
// definition, and deriving it here removes the chance of being handed a pair
// that does not belong together.
func NewScramClient(user string, clientKey []byte) (*ScramClient, error) {
	if len(clientKey) != sha256.Size {
		return nil, fmt.Errorf("%w: client key is %d bytes, want %d",
			ErrAuthFailed, len(clientKey), sha256.Size)
	}
	stored := sha256.Sum256(clientKey)
	return &ScramClient{
		user:      user,
		clientKey: clientKey,
		storedKey: stored[:],
	}, nil
}

// First returns the client-first message.
//
// The GS2 header is "n,," — no channel binding. Pontus cannot bind to a channel
// it did not establish, and claiming otherwise would be a lie the backend would
// act on.
func (c *ScramClient) First() (string, error) {
	nonce, err := newNonce()
	if err != nil {
		return "", err
	}
	c.nonce = nonce
	c.firstBare = "n=" + saslPrep(c.user) + ",r=" + nonce
	return "n,," + c.firstBare, nil
}

// Final answers the server-first message with a proof.
func (c *ScramClient) Final(serverFirst string) (string, error) {
	if c.firstBare == "" {
		return "", fmt.Errorf("%w: Final before First", ErrProtocol)
	}

	combined, err := attr(serverFirst, 'r')
	if err != nil {
		return "", err
	}
	// The server must extend our nonce, not replace it. A server that returns
	// something else is either broken or replaying another exchange at us.
	if len(combined) <= len(c.nonce) || combined[:len(c.nonce)] != c.nonce {
		return "", fmt.Errorf("%w: server nonce does not extend the client's", ErrAuthFailed)
	}

	// The salt and iteration count are the server's, and are only used to
	// confirm it is talking about a credential we can answer for. We already
	// hold ClientKey, so no derivation from a password happens here.
	if _, err := attr(serverFirst, 's'); err != nil {
		return "", err
	}
	iterText, err := attr(serverFirst, 'i')
	if err != nil {
		return "", err
	}
	if iterations, convErr := strconv.Atoi(iterText); convErr != nil || iterations <= 0 {
		return "", fmt.Errorf("%w: iteration count %q", ErrProtocol, iterText)
	}

	withoutProof := "c=biws,r=" + combined
	c.authMsg = c.firstBare + "," + serverFirst + "," + withoutProof

	signature := hmacSHA256(c.storedKey, c.authMsg)
	proof := xorBytes(c.clientKey, signature)

	return withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof), nil
}

// VerifyServer checks the server's final message against the known ServerKey.
//
// Skipping this would let anything that can answer on the backend's address
// complete an exchange, which is the half of SCRAM that authenticates the
// *server* to us. It needs ServerKey from the same verifier ClientKey came
// from.
func (c *ScramClient) VerifyServer(serverFinal string, serverKey []byte) error {
	if c.authMsg == "" {
		return fmt.Errorf("%w: VerifyServer before Final", ErrProtocol)
	}

	got, err := attr(serverFinal, 'v')
	if err != nil {
		// A server that failed the exchange sends e=<reason> instead.
		if reason, rerr := attr(serverFinal, 'e'); rerr == nil {
			return fmt.Errorf("%w: server said %q", ErrAuthFailed, reason)
		}
		return err
	}

	want := hmacSHA256(serverKey, c.authMsg)
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		return fmt.Errorf("%w: server signature is not base64", ErrProtocol)
	}
	if subtle.ConstantTimeCompare(decoded, want) != 1 {
		return fmt.Errorf("%w: server signature does not verify", ErrAuthFailed)
	}
	return nil
}

// saslPrep escapes the characters SCRAM reserves in a username.
//
// Full SASLprep normalisation is not applied: PostgreSQL only requires that ','
// and '=' be escaped, and applying a Unicode profile to a name that has already
// been matched against pg_authid could change which role is meant.
func saslPrep(user string) string {
	out := make([]byte, 0, len(user))
	for i := range len(user) {
		switch user[i] {
		case '=':
			out = append(out, '=', '3', 'D')
		case ',':
			out = append(out, '=', '2', 'C')
		default:
			out = append(out, user[i])
		}
	}
	return string(out)
}

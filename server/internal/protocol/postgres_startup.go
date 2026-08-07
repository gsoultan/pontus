package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// PostgreSQL startup-phase constants.
const (
	sslRequestCode    = 80877103
	gssEncRequestCode = 80877104
	cancelRequestCode = 80877102

	// Startup packets are small; anything larger is a client lying about its
	// length prefix rather than a real StartupMessage.
	maxStartupLen = 10 << 10

	// A single backend message during the startup phase. SASL and error payloads
	// are far below this; a larger frame means the stream is out of sync.
	maxStartupMsgLen = 1 << 20
)

// startupPacket is an untyped, length-prefixed message: the client's
// StartupMessage, SSLRequest, GSSENCRequest or CancelRequest.
type startupPacket struct {
	raw  []byte // the complete packet, length prefix included
	code uint32 // the first 4 bytes of the body — protocol version or a request code
}

// readStartupPacket reads one untyped length-prefixed packet.
//
// The length prefix is bounded before it is trusted: it arrives from an
// unauthenticated client, and using it to size an allocation is how a proxy is
// turned into a memory-exhaustion primitive.
func readStartupPacket(r io.Reader) (*startupPacket, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read startup length: %w", err)
	}

	length := int(binary.BigEndian.Uint32(lenBuf[:]))
	if length < 8 || length > maxStartupLen {
		return nil, fmt.Errorf("startup packet length %d out of range", length)
	}

	raw := make([]byte, length)
	copy(raw, lenBuf[:])
	if _, err := io.ReadFull(r, raw[4:]); err != nil {
		return nil, fmt.Errorf("read startup body: %w", err)
	}

	return &startupPacket{raw: raw, code: binary.BigEndian.Uint32(raw[4:8])}, nil
}

// message is one typed protocol message: a type byte, a length prefix that
// covers itself, and a body.
type message struct {
	kind byte
	raw  []byte // type byte + length prefix + body
}

func (m *message) body() []byte { return m.raw[5:] }

// readMessage reads exactly one typed message. Framing each message explicitly
// is what makes the startup exchange correct: a single Read may carry several
// messages or half of one, so scanning a raw buffer for a type byte finds
// whatever happens to sit at a chunk boundary.
func readMessage(r io.Reader) (*message, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}

	length := int(binary.BigEndian.Uint32(head[1:5]))
	if length < 4 || length > maxStartupMsgLen {
		return nil, fmt.Errorf("message length %d out of range for type %q", length, head[0])
	}

	raw := make([]byte, 1+length)
	copy(raw, head[:])
	if _, err := io.ReadFull(r, raw[5:]); err != nil {
		return nil, err
	}

	return &message{kind: head[0], raw: raw}, nil
}

// authRequest reports the sub-type of an Authentication ('R') message and
// whether the server is waiting for the client to answer it.
//
// This is the distinction the previous implementation missed. It forwarded
// server messages to the client and never carried the client's reply back, so
// every method that takes more than one round trip — SCRAM (the PostgreSQL
// default since 14), md5, cleartext — deadlocked with both sides waiting.
func authRequest(m *message) (subtype uint32, needsClientReply bool) {
	if m.kind != 'R' || len(m.body()) < 4 {
		return 0, false
	}

	subtype = binary.BigEndian.Uint32(m.body()[:4])
	switch subtype {
	case 0: // AuthenticationOk
		return subtype, false
	case 2, // KerberosV5
		3,  // CleartextPassword
		5,  // MD5Password
		7,  // GSS
		8,  // GSSContinue
		9,  // SSPI
		10, // SASL
		11: // SASLContinue
		return subtype, true
	case 12: // SASLFinal — the server follows it with AuthenticationOk itself
		return subtype, false
	default:
		return subtype, false
	}
}

// relayAuth carries the startup exchange between client and server to
// completion, in both directions, until the server reports ReadyForQuery.
func relayAuth(client, server net.Conn) error {
	for {
		msg, err := readMessage(server)
		if err != nil {
			return fmt.Errorf("read from server during handshake: %w", err)
		}

		if _, err := client.Write(msg.raw); err != nil {
			return fmt.Errorf("forward server message to client: %w", err)
		}

		switch msg.kind {
		case 'Z': // ReadyForQuery — the startup phase is over.
			return nil
		case 'E': // ErrorResponse — the server rejected this client.
			return fmt.Errorf("server rejected the connection during handshake")
		case 'R':
			if _, needsReply := authRequest(msg); !needsReply {
				continue
			}
			reply, rerr := readMessage(client)
			if rerr != nil {
				return fmt.Errorf("read auth reply from client: %w", rerr)
			}
			if _, werr := server.Write(reply.raw); werr != nil {
				return fmt.Errorf("forward auth reply to server: %w", werr)
			}
		}
	}
}

// extractStartupParams pulls the "user" and "database" parameters out of a
// StartupMessage body.
//
// The value is attacker-chosen and unauthenticated at this point — the backend
// has not vetted it yet — so callers must treat it as a claim, and must not use
// it as an unbounded map key.
func extractStartupParams(pkt *startupPacket) (user, database string) {
	if len(pkt.raw) <= 8 {
		return "", ""
	}

	payload := pkt.raw[8:]
	for {
		idx := indexZero(payload)
		if idx <= 0 {
			return user, database
		}
		key := string(payload[:idx])
		payload = payload[idx+1:]

		idx = indexZero(payload)
		if idx < 0 {
			return user, database
		}
		value := string(payload[:idx])
		payload = payload[idx+1:]

		switch key {
		case "user":
			user = value
		case "database":
			database = value
		}
	}
}

// ParseStatementName returns the prepared-statement name carried by a Parse
// ('P') message, or "" for anything else and for the unnamed statement.
//
// The gateway uses this to record a statement against the backend connection
// that actually parsed it. Without that, only *replayed* statements are tracked,
// and a session returning to a connection it used earlier replays a statement
// that connection already has.
func ParseStatementName(data []byte) string {
	if len(data) < 6 || data[0] != 'P' {
		return ""
	}

	end := indexZero(data[5:])
	if end <= 0 { // -1 = unterminated, 0 = the unnamed statement
		return ""
	}
	return string(data[5 : 5+end])
}

func indexZero(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}

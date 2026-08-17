package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// PostgreSQL authentication message framing.
//
// The Authentication* messages all share the 'R' tag and are told apart by a
// four-byte subtype, which is why they are decoded together rather than one per
// function.

const (
	authOK                 = 0
	authCleartextPassword  = 3
	authMD5Password        = 5
	authSASL               = 10
	authSASLContinue       = 11
	authSASLFinal          = 12
	saslMechanismSCRAM     = "SCRAM-SHA-256"
	saslMechanismSCRAMPlus = "SCRAM-SHA-256-PLUS"
)

// ErrUnsupportedAuth marks an authentication method Pontus cannot perform on a
// client's behalf.
var ErrUnsupportedAuth = errors.New("unsupported authentication method")

// AuthRequest is a decoded Authentication* message from a server.
type AuthRequest struct {
	// Type is the subtype: authOK, authMD5Password, authSASL and so on.
	Type int32

	// Salt is the four-byte challenge for md5.
	Salt [4]byte

	// Mechanisms are the SASL mechanisms a server offers.
	Mechanisms []string

	// Data is the SASL payload for Continue and Final.
	Data []byte
}

// WriteAuthSASL asks a client to authenticate with SCRAM-SHA-256.
//
// Only the non-channel-binding mechanism is offered. Advertising
// SCRAM-SHA-256-PLUS would invite a client to bind the exchange to the TLS
// channel it has with Pontus — a channel that terminates here and cannot be
// reproduced toward a backend, so the binding would be a promise Pontus cannot
// keep.
func WriteAuthSASL(w io.Writer) error {
	payload := make([]byte, 0, 32)
	payload = binary.BigEndian.AppendUint32(payload, authSASL)
	payload = append(payload, saslMechanismSCRAM...)
	payload = append(payload, 0) // terminate the mechanism
	payload = append(payload, 0) // terminate the list
	return writeTagged(w, 'R', payload)
}

// WriteAuthSASLContinue sends a SASL challenge.
func WriteAuthSASLContinue(w io.Writer, data string) error {
	payload := binary.BigEndian.AppendUint32(make([]byte, 0, len(data)+4), authSASLContinue)
	return writeTagged(w, 'R', append(payload, data...))
}

// WriteAuthSASLFinal sends the server's final SASL message.
func WriteAuthSASLFinal(w io.Writer, data string) error {
	payload := binary.BigEndian.AppendUint32(make([]byte, 0, len(data)+4), authSASLFinal)
	return writeTagged(w, 'R', append(payload, data...))
}

// WriteAuthMD5 challenges a client to answer with an md5 response.
func WriteAuthMD5(w io.Writer, salt [4]byte) error {
	payload := binary.BigEndian.AppendUint32(make([]byte, 0, 8), authMD5Password)
	return writeTagged(w, 'R', append(payload, salt[:]...))
}

// ReadPasswordMessage reads a client's PasswordMessage.
//
// The same 'p' tag carries SASL replies; which one it is depends on what was
// asked for, so the caller decides how to read it.
func ReadPasswordMessage(r io.Reader) (string, error) {
	tag, body, err := readTagged(r)
	if err != nil {
		return "", err
	}
	if tag != 'p' {
		return "", fmt.Errorf("expected a password message, got %q", string(tag))
	}
	// The value is a C string; drop the terminator if present.
	if n := indexByte(body, 0); n >= 0 {
		body = body[:n]
	}
	return string(body), nil
}

// WritePasswordMessage sends a password response to a server.
func WritePasswordMessage(w io.Writer, response string) error {
	return writeTagged(w, 'p', append([]byte(response), 0))
}

// WriteAuthOK tells a client it is authenticated.
func WriteAuthOK(w io.Writer) error {
	return writeTagged(w, 'R', binary.BigEndian.AppendUint32(nil, authOK))
}

// WriteSASLInitialResponse sends a client's first SASL message to a server.
func WriteSASLInitialResponse(w io.Writer, mechanism, clientFirst string) error {
	payload := make([]byte, 0, len(mechanism)+len(clientFirst)+8)
	payload = append(payload, mechanism...)
	payload = append(payload, 0)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(clientFirst)))
	payload = append(payload, clientFirst...)
	return writeTagged(w, 'p', payload)
}

// WriteSASLResponse sends a client's subsequent SASL message to a server.
func WriteSASLResponse(w io.Writer, data string) error {
	return writeTagged(w, 'p', []byte(data))
}

// ReadAuthRequest reads one Authentication* message from a server.
func ReadAuthRequest(r io.Reader) (*AuthRequest, error) {
	tag, body, err := readTagged(r)
	if err != nil {
		return nil, err
	}
	if tag == 'E' {
		return nil, fmt.Errorf("server refused the connection: %s", errorFields(body))
	}
	if tag != 'R' {
		return nil, fmt.Errorf("expected an authentication message, got %q", string(tag))
	}
	if len(body) < 4 {
		return nil, errors.New("authentication message is truncated")
	}

	req := &AuthRequest{Type: int32(binary.BigEndian.Uint32(body[:4]))}
	rest := body[4:]

	switch req.Type {
	case authMD5Password:
		if len(rest) < 4 {
			return nil, errors.New("md5 challenge has no salt")
		}
		copy(req.Salt[:], rest[:4])
	case authSASL:
		for len(rest) > 0 {
			end := indexByte(rest, 0)
			if end <= 0 {
				break
			}
			req.Mechanisms = append(req.Mechanisms, string(rest[:end]))
			rest = rest[end+1:]
		}
	case authSASLContinue, authSASLFinal:
		req.Data = rest
	}
	return req, nil
}

// ReadSASLResponse reads a client's SASL reply.
//
// initial reports whether this is the first one, which carries a mechanism name
// and a length prefix that later messages do not.
func ReadSASLResponse(r io.Reader, initial bool) (mechanism string, data []byte, err error) {
	tag, body, err := readTagged(r)
	if err != nil {
		return "", nil, err
	}
	if tag != 'p' {
		return "", nil, fmt.Errorf("expected a SASL response, got %q", string(tag))
	}

	if !initial {
		return "", body, nil
	}

	end := indexByte(body, 0)
	if end < 0 {
		return "", nil, errors.New("SASL initial response has no mechanism")
	}
	mechanism = string(body[:end])
	rest := body[end+1:]
	if len(rest) < 4 {
		return "", nil, errors.New("SASL initial response has no length")
	}

	length := int32(binary.BigEndian.Uint32(rest[:4]))
	rest = rest[4:]
	// -1 means "no data", which is legal in the protocol and never legal here:
	// a SCRAM exchange cannot begin without a client-first message.
	if length < 0 || int(length) > len(rest) {
		return "", nil, fmt.Errorf("SASL initial response claims %d bytes, has %d", length, len(rest))
	}
	return mechanism, rest[:length], nil
}

// writeTagged frames a message: one tag byte, a length covering itself, a body.
func writeTagged(w io.Writer, tag byte, body []byte) error {
	out := make([]byte, 0, len(body)+5)
	out = append(out, tag)
	out = binary.BigEndian.AppendUint32(out, uint32(len(body)+4))
	out = append(out, body...)
	_, err := w.Write(out)
	return err
}

// readTagged reads one tagged message.
//
// The length is bounded before it is used to size an allocation: it comes from
// the other end of a socket, and a four-byte length is four gigabytes if it is
// believed.
func readTagged(r io.Reader) (tag byte, body []byte, err error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	length := int64(binary.BigEndian.Uint32(header[1:]))
	if length < 4 || length > maxAuthMessage {
		return 0, nil, fmt.Errorf("authentication message length %d is out of range", length)
	}

	body = make([]byte, length-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return header[0], body, nil
}

// maxAuthMessage bounds an authentication message. A SCRAM exchange is a few
// hundred bytes; anything approaching this is not one.
const maxAuthMessage = 64 * 1024

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// errorFields renders an ErrorResponse for a message, keeping the fields an
// operator needs and dropping the rest.
func errorFields(body []byte) string {
	var severity, code, message string
	for len(body) > 0 {
		field := body[0]
		if field == 0 {
			break
		}
		end := indexByte(body[1:], 0)
		if end < 0 {
			break
		}
		value := string(body[1 : 1+end])
		switch field {
		case 'S':
			severity = value
		case 'C':
			code = value
		case 'M':
			message = value
		}
		body = body[end+2:]
	}
	return fmt.Sprintf("%s %s: %s", severity, code, message)
}

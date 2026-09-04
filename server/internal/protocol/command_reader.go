package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MaxCommandBytes bounds one message read from a client in the query phase.
//
// The administration console answers commands a person types, not application
// SQL: a few hundred bytes is a long one. The bound exists because the length
// prefix arrives from a socket and using it to size an allocation without
// checking it is how a proxy becomes a memory-exhaustion primitive.
const MaxCommandBytes = 64 * 1024

// ReadCommand reads one typed message from a client that has completed its
// startup, returning the message tag and its body.
func ReadCommand(r io.Reader) (tag byte, body []byte, err error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	length := int64(binary.BigEndian.Uint32(header[1:]))
	if length < 4 || length > MaxCommandBytes {
		return 0, nil, fmt.Errorf("command message length %d is out of range", length)
	}

	body = make([]byte, length-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return header[0], body, nil
}

// QueryText extracts the statement from a simple-query ('Q') message body,
// which is a single null-terminated string.
func QueryText(body []byte) string {
	if i := indexByte(body, 0); i >= 0 {
		return string(body[:i])
	}
	return string(body)
}

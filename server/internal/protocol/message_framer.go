package protocol

import "encoding/binary"

// MessageFramer reports how long a client's message is, so a caller can tell a
// complete one from the first piece of a larger one.
//
// This exists because a TCP read is not a message. The transaction loop read
// once into a 32 KiB buffer and forwarded whatever arrived as though it were a
// whole request — so a statement larger than the buffer was sent to the backend
// in a fragment, and the backend, still waiting for the rest, sent nothing
// back. The session then sat until query_timeout killed it. Any statement over
// 32 KiB failed that way: a bulk INSERT, a generated IN list, a long text
// parameter.
//
// Optional, because framing is protocol-specific and a handler that cannot do
// it should keep the older behaviour rather than be given a wrong answer.
type MessageFramer interface {
	// MessageLength returns the total size of the message beginning at data[0],
	// header included.
	//
	// ok is false when data is too short to tell yet, which is a request for
	// more bytes rather than an error.
	MessageLength(data []byte) (total int, ok bool)
}

// postgresHeaderSize is the tag byte plus the four-byte length.
const postgresHeaderSize = 5

// MessageLength implements MessageFramer.
//
// A regular message is a one-byte tag, then an int32 length covering itself and
// the body. The startup packet has no tag — but it is read during the startup
// phase, which frames itself, so it never reaches here.
func (p *PostgresHandler) MessageLength(data []byte) (total int, ok bool) {
	if len(data) < postgresHeaderSize {
		return 0, false
	}

	length := int(binary.BigEndian.Uint32(data[1:postgresHeaderSize]))
	if length < 4 {
		// Not a length this can follow. Report the bytes in hand as the whole
		// message so the caller stops waiting for more — the same fail-fast the
		// query path takes on a malformed prefix.
		return len(data), true
	}
	return 1 + length, true
}

// PostgresFramer frames PostgreSQL messages without needing a whole handler.
//
// The handler carries connection state and configuration; framing needs
// neither, and a caller that only wants to find message boundaries should not
// have to construct one.
type PostgresFramer struct{}

// MessageLength implements MessageFramer.
func (PostgresFramer) MessageLength(data []byte) (total int, ok bool) {
	return (*PostgresHandler)(nil).MessageLength(data)
}

package protocol

import (
	"encoding/binary"
	"errors"
	"net"
)

// WritePostgresError sends a PostgreSQL ErrorResponse followed by a
// ReadyForQuery, so the client reports a real error instead of seeing the
// connection drop.
//
// Fields follow the wire format: a severity/code/message triplet, each
// null-terminated and tagged, then a trailing zero byte.
//
// 0A000 is feature_not_supported, which is what a client should see when the
// proxy declines to carry a connection mode rather than failing at it.
func WritePostgresError(conn net.Conn, code, message string) error {
	body := make([]byte, 0, len(message)+len(code)+32)
	body = appendField(body, 'S', "ERROR")
	body = appendField(body, 'V', "ERROR")
	body = appendField(body, 'C', code)
	body = appendField(body, 'M', message)
	body = append(body, 0)

	frame := make([]byte, 0, len(body)+11)
	frame = append(frame, 'E')
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(body)+4))
	frame = append(frame, body...)

	// ReadyForQuery('I') so a client that survives the error is not left
	// waiting for a message that never arrives.
	frame = append(frame, 'Z')
	frame = binary.BigEndian.AppendUint32(frame, 5)
	frame = append(frame, 'I')

	_, err := conn.Write(frame)
	return err
}

// WriteStartupError reports a failure that happened before the client was
// authenticated.
//
// Deliberately without the ReadyForQuery that WritePostgresError appends. That
// is right *during a session* — a client that survives a query error is waiting
// for one — but during startup the client has not had AuthenticationOk and is
// not in the query phase. A server that sends ReadyForQuery there is describing
// a state the connection is not in, and PostgreSQL itself sends the error and
// closes.
func WriteStartupError(conn net.Conn, code, message string) error {
	body := make([]byte, 0, len(message)+len(code)+32)
	body = appendField(body, 'S', "FATAL")
	body = appendField(body, 'V', "FATAL")
	body = appendField(body, 'C', code)
	body = appendField(body, 'M', message)
	body = append(body, 0)

	frame := make([]byte, 0, len(body)+5)
	frame = append(frame, 'E')
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(body)+4))
	frame = append(frame, body...)

	_, err := conn.Write(frame)
	return err
}

func appendField(dst []byte, tag byte, value string) []byte {
	dst = append(dst, tag)
	dst = append(dst, value...)
	return append(dst, 0)
}

// ErrReplicationRequested reports that the client asked for a replication
// stream, so the handshake stopped and handed the decision back.
//
// It is a distinct error rather than a failure: the backend connection is
// untouched, the client has not been answered, and the gateway still has to
// choose the node holding the slot before the startup packet can be forwarded.
var ErrReplicationRequested = errors.New("replication stream requested")

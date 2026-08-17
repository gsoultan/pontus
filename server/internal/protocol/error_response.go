package protocol

import "encoding/binary"

// PostgreSQL SQLSTATE codes Pontus raises on its own behalf.
//
// A proxy that fails a client's query has to say so in the client's language.
// Closing the socket is not a diagnosis: every driver renders it as some
// variant of "connection closed" or EOF, which sends the operator looking at
// the network and the database rather than at the proxy setting that actually
// fired.
const (
	// SQLStateQueryCanceled is what PostgreSQL itself returns when
	// statement_timeout fires. Drivers already map it to a timeout error, so
	// reusing it means a Pontus-side timeout surfaces the same way as a
	// database-side one.
	SQLStateQueryCanceled = "57014"

	// SQLStateAdminShutdown is what a server sends when it is terminating the
	// session rather than just the statement.
	SQLStateAdminShutdown = "57P01"
)

// Severity levels. FATAL tells the client the session is over and it should
// not expect a ReadyForQuery; ERROR means the statement failed but the session
// continues.
const (
	SeverityError = "ERROR"
	SeverityFatal = "FATAL"
)

// ErrorResponse builds a PostgreSQL ErrorResponse ('E') message.
//
// Wire format is a byte tag, a length, then a sequence of NUL-terminated
// fields each introduced by a one-byte code, closed by a zero byte:
//
//	'E' int32(len) 'S' severity 0 'V' severity 0 'C' sqlstate 0 'M' message 0 0
//
// 'S' is the localized severity and 'V' the non-localized one; PostgreSQL has
// sent both since protocol 3.0 minor version 9.6, and libpq reads 'V' in
// preference. Both are emitted so older and newer clients agree.
func ErrorResponse(severity, code, message string) []byte {
	fields := []struct {
		tag   byte
		value string
	}{
		{'S', severity},
		{'V', severity},
		{'C', code},
		{'M', message},
	}

	// 4 length bytes, then each field as tag + value + NUL, then the terminator.
	size := 4 + 1
	for _, f := range fields {
		size += 1 + len(f.value) + 1
	}

	out := make([]byte, 0, 1+size)
	out = append(out, 'E')
	out = binary.BigEndian.AppendUint32(out, uint32(size))
	for _, f := range fields {
		out = append(out, f.tag)
		out = append(out, f.value...)
		out = append(out, 0)
	}
	return append(out, 0)
}

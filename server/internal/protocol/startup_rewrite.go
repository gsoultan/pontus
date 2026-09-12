package protocol

import (
	"encoding/binary"
	"fmt"
)

// RewriteStartupDatabase returns the client's startup packet with its
// "database" parameter replaced.
//
// Aliasing has to reach the backend, and in passthrough the backend is handed
// the client's own packet verbatim. Rewriting only the parsed field would route
// the pool by the alias while the connection actually opened the database the
// client named — the two halves disagreeing about which database a connection
// belongs to is the shape of finding A11, one identity being served on
// another's connection.
//
// The packet is rebuilt rather than patched in place: the replacement is a
// different length, and the leading length prefix covers itself.
func RewriteStartupDatabase(raw []byte, database string) ([]byte, error) {
	if len(raw) <= 8 {
		return nil, fmt.Errorf("startup packet is too short to carry parameters")
	}

	// Version and everything before the parameters is copied unchanged.
	out := make([]byte, 0, len(raw)+len(database))
	out = append(out, raw[4:8]...)

	payload := raw[8:]
	var replaced bool
	for {
		idx := indexZero(payload)
		if idx <= 0 {
			// The terminating zero byte that ends the parameter list.
			break
		}
		key := payload[:idx]
		payload = payload[idx+1:]

		idx = indexZero(payload)
		if idx < 0 {
			return nil, fmt.Errorf("startup parameter %q has no value", key)
		}
		value := payload[:idx]
		payload = payload[idx+1:]

		if string(key) == "database" {
			value = []byte(database)
			replaced = true
		}

		out = append(out, key...)
		out = append(out, 0)
		out = append(out, value...)
		out = append(out, 0)
	}

	// A client may omit "database" entirely, in which case PostgreSQL defaults
	// it to the user name. An alias still has to be honoured, so the parameter
	// is added rather than the rewrite silently doing nothing.
	if !replaced {
		out = append(out, "database"...)
		out = append(out, 0)
		out = append(out, database...)
		out = append(out, 0)
	}
	out = append(out, 0)

	packet := binary.BigEndian.AppendUint32(make([]byte, 0, len(out)+4), uint32(len(out)+4))
	return append(packet, out...), nil
}

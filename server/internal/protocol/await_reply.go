package protocol

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"
)

// awaitReply reads until the backend has finished answering a statement Pontus
// sent on its own behalf — a session-state replay, a prepared-statement replay.
//
// Two things it does that the loops it replaces did not.
//
// It honours the context. Those loops took a ctx and never used it, so a
// backend that accepted the statement and then went quiet held the session
// forever with no deadline anywhere.
//
// And it reports an ErrorResponse as an error. They scanned for ReadyForQuery
// and skipped straight past a rejection, so a `SET search_path` the backend
// refused was reported as a successful replay — and the session then ran
// against the wrong schema, or with the wrong role, on a connection Pontus
// believed it had configured.
func awaitReply(ctx context.Context, conn net.Conn, buf []byte, what string) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	}

	scan := newReplyScanner(ResponseEnd{OnReadyForQuery: true})
	var failure string

	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if failure == "" {
				failure = errorText(buf[:n])
			}
			if done, _ := scan.feed(buf[:n]); done {
				if scan.SawError() {
					return fmt.Errorf("%s: backend refused it: %s", what, orUnknown(failure))
				}
				return nil
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
	}
}

// errorText pulls the human-readable message out of an ErrorResponse.
//
// Best effort, and deliberately so: it is used only to explain a failure that
// has already been detected by the framing scanner, so a miss costs a less
// specific log line and nothing else. An ErrorResponse field is a one-byte
// code then a NUL-terminated value; 'M' is the primary message.
func errorText(chunk []byte) string {
	for i := 0; i+5 < len(chunk); i++ {
		if chunk[i] != 'E' {
			continue
		}
		length := int(uint32(chunk[i+1])<<24 | uint32(chunk[i+2])<<16 |
			uint32(chunk[i+3])<<8 | uint32(chunk[i+4]))
		if length < 4 || i+1+length > len(chunk) {
			continue
		}
		body := chunk[i+5 : i+1+length]
		for len(body) > 1 {
			field := body[0]
			value, rest, found := bytes.Cut(body[1:], []byte{0})
			if !found {
				break
			}
			if field == 'M' {
				return string(value)
			}
			body = rest
		}
	}
	return ""
}

func orUnknown(s string) string {
	if s == "" {
		return "no message"
	}
	return s
}

package proxy

import (
	"bytes"
	"errors"
)

// defaultMaxCaptureBytes bounds how much of one reply Pontus will hold in
// memory so it can be cached or shared with collapsed requests.
//
// Without a bound, `SELECT * FROM events` buffers the entire result set in the
// proxy's heap — per concurrent client — while streaming it to the one client
// that asked. The database streams results precisely so that nobody has to hold
// them, and a pooler that holds them anyway is the component that runs out of
// memory first.
//
// 8 MiB is comfortably above any result worth caching and far below a number
// that threatens the process.
const defaultMaxCaptureBytes = 8 << 20

// errCaptureTooLarge marks a reply that outgrew the capture bound.
//
// It is reported to collapsed followers as a failure so they run the query
// themselves. A follower handed a truncated buffer would write a partial reply
// and leave its client waiting for the rest of a message that will never
// arrive — worse than not collapsing at all.
var errCaptureTooLarge = errors.New("response too large to cache or share")

// responseCapture accumulates a reply for the cache and the request collapser,
// up to a bound, and records whether it gave up.
//
// Giving up is not a failure of the query: the client is still streamed every
// byte. Only the *remembering* stops.
type responseCapture struct {
	buf        *bytes.Buffer
	limit      int
	overflowed bool
}

func newResponseCapture(buf *bytes.Buffer, limit int) *responseCapture {
	if limit <= 0 {
		limit = defaultMaxCaptureBytes
	}
	return &responseCapture{buf: buf, limit: limit}
}

// Write records a chunk unless the bound has been passed.
//
// On the first overflow the buffer is released, not just abandoned. Holding
// eight megabytes for the life of a session that will never use them is the
// same leak in a smaller size.
func (c *responseCapture) Write(chunk []byte) {
	if c == nil || c.overflowed {
		return
	}
	if c.buf.Len()+len(chunk) > c.limit {
		c.overflowed = true
		c.buf.Reset()
		return
	}
	c.buf.Write(chunk)
}

// Bytes returns the captured reply, or nil if it was abandoned.
func (c *responseCapture) Bytes() []byte {
	if c == nil || c.overflowed {
		return nil
	}
	return c.buf.Bytes()
}

// Err reports why the capture cannot be used, or nil if it can.
func (c *responseCapture) Err() error {
	if c != nil && c.overflowed {
		return errCaptureTooLarge
	}
	return nil
}

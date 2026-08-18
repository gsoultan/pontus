package protocol

// Knowing when a reply has ended.
//
// A client ends an extended-protocol batch with either Sync or Flush, and the
// two mean different things. Sync closes the implicit transaction and the server
// answers with ReadyForQuery. Flush only asks the server to send what it has —
// there is no terminator at all, and no ReadyForQuery is coming.
//
// Pontus decided a reply was finished by looking for ReadyForQuery and nothing
// else, so a Flush-terminated batch left it blocked on a read forever: the
// client hung, the connection stayed checked out, and nothing below
// query_timeout noticed. asyncpg prepares that way, and so does libpq in
// pipeline mode, so this was not a corner case — it was every user of those
// clients.

// Frontend message types that produce a reply, and what ends that reply.
const (
	feParse       = 'P'
	feBind        = 'B'
	feDescribe    = 'D'
	feExecute     = 'E'
	feClose       = 'C'
	feFunctionCal = 'F'
	feSync        = 'S'
	feFlush       = 'H'
	feQuery       = 'Q'
)

// ResponseEnd describes how to recognise the end of a reply to one request.
type ResponseEnd struct {
	// OnReadyForQuery is set when the request contained a Sync, or was a simple
	// query. Both are answered with ReadyForQuery.
	OnReadyForQuery bool

	// Tags are the backend message types that end the reply when there is no
	// ReadyForQuery to wait for. Any of them finishes it.
	Tags []byte
}

// awaited reports whether tag ends the reply.
func (r ResponseEnd) awaited(tag byte) bool {
	// An ErrorResponse ends any exchange, whatever was asked for.
	if tag == 'E' {
		return true
	}
	for _, want := range r.Tags {
		if tag == want {
			return true
		}
	}
	return false
}

// ResponseEndFor works out how the reply to this request will end.
//
// A Sync anywhere in the batch means ReadyForQuery, because the server answers
// every preceding message and then sends it. Otherwise the reply ends with the
// answer to the *last* message that produces one — everything before it will
// have been answered by the time that arrives.
func (p *PostgresHandler) ResponseEndFor(request []byte) ResponseEnd {
	var last byte
	var describeTarget byte

	for i := 0; i+5 <= len(request); {
		tag := request[i]
		length := int(uint32(request[i+1])<<24 | uint32(request[i+2])<<16 |
			uint32(request[i+3])<<8 | uint32(request[i+4]))
		if length < 4 {
			break
		}

		switch tag {
		case feSync, feQuery:
			// Both are answered with ReadyForQuery, so nothing later matters.
			return ResponseEnd{OnReadyForQuery: true}
		case feParse, feBind, feExecute, feClose, feFunctionCal:
			last = tag
		case feDescribe:
			last = tag
			if i+5 < len(request) {
				describeTarget = request[i+5]
			}
		}

		next := i + 1 + length
		if next <= i {
			break
		}
		i = next
	}

	return ResponseEnd{Tags: terminatorsFor(last, describeTarget)}
}

// terminatorsFor maps a frontend message to the backend messages that answer it.
func terminatorsFor(last, describeTarget byte) []byte {
	switch last {
	case feParse:
		return []byte{'1'} // ParseComplete
	case feBind:
		return []byte{'2'} // BindComplete
	case feDescribe:
		if describeTarget == 'S' {
			// A statement description is ParameterDescription then either a
			// RowDescription or NoData — the latter two end it.
			return []byte{'T', 'n'}
		}
		return []byte{'T', 'n'}
	case feExecute:
		// CommandComplete, PortalSuspended or EmptyQueryResponse.
		return []byte{'C', 's', 'I'}
	case feClose:
		return []byte{'3'} // CloseComplete
	case feFunctionCal:
		return []byte{'V'} // FunctionCallResponse
	default:
		// Nothing that produces a reply — a lone Flush, or a message type this
		// does not model. Nil means "do not wait", which is the safe answer:
		// blocking for a reply that is not coming is the bug being fixed.
		return nil
	}
}

// replyScanner walks backend messages across reads that may split them.
//
// Only tags matter here, and the transaction status byte of ReadyForQuery, so
// message bodies are skipped rather than buffered — the largest thing carried
// between reads is a four-byte length.
type replyScanner struct {
	end ResponseEnd

	// header holds a partial message header when a read ends mid-header.
	header []byte

	// skip is how many bytes of the current message body are still to come.
	skip int

	// wantStatus is set when a ReadyForQuery header has been seen but its
	// status byte has not arrived yet.
	wantStatus bool

	// sawError records that the backend sent an ErrorResponse. Only meaningful
	// to callers that act on a failed reply — the proxy forwards the bytes
	// either way and lets the client decide.
	sawError bool
}

// NewReplyScanner builds a scanner for one request's reply.
func NewReplyScanner(end ResponseEnd) *ReplyScanner { return newReplyScanner(end) }

// ReplyScanner is exported so the gateway can hold one across reads.
type ReplyScanner = replyScanner

// Feed consumes a chunk. See feed.
func (s *replyScanner) Feed(chunk []byte) (bool, TransactionState) { return s.feed(chunk) }

// SawError reports whether an ErrorResponse appeared in the reply.
//
// Observed on the framing pass because it is the only place message boundaries
// are known — searching the raw bytes for an 'E' finds every byte of row data
// that happens to be 0x45.
func (s *replyScanner) SawError() bool { return s.sawError }

func newReplyScanner(end ResponseEnd) *replyScanner {
	return &replyScanner{end: end, header: make([]byte, 0, 5)}
}

// feed consumes a chunk and reports whether the reply has ended, along with the
// transaction state if a ReadyForQuery was seen.
func (s *replyScanner) feed(chunk []byte) (done bool, state TransactionState) {
	state = StatePartial

	for len(chunk) > 0 {
		if s.skip > 0 {
			n := min(s.skip, len(chunk))
			if s.wantStatus {
				// The first body byte of ReadyForQuery is the transaction status.
				state = transactionStatus(chunk[0])
				s.wantStatus = false
				done = true
			}
			s.skip -= n
			chunk = chunk[n:]
			continue
		}

		if len(s.header) < 5 {
			n := min(5-len(s.header), len(chunk))
			s.header = append(s.header, chunk[:n]...)
			chunk = chunk[n:]
			if len(s.header) < 5 {
				return done, state
			}
		}

		tag := s.header[0]
		length := int(uint32(s.header[1])<<24 | uint32(s.header[2])<<16 |
			uint32(s.header[3])<<8 | uint32(s.header[4]))
		s.header = s.header[:0]

		if length < 4 {
			// Not a frame this can follow. Stop rather than guess: the caller
			// still forwards the bytes, it just stops trying to find the end.
			return true, state
		}
		s.skip = length - 4

		if tag == 'E' {
			s.sawError = true
		}

		switch {
		case tag == 'Z':
			if s.skip > 0 {
				s.wantStatus = true
			} else {
				done = true
			}
		case !s.end.OnReadyForQuery && s.end.awaited(tag):
			done = true
		}
	}

	return done, state
}

func transactionStatus(b byte) TransactionState {
	switch b {
	case 'I':
		return StateIdle
	case 'T', 'B':
		return StateInTransaction
	case 'E':
		return StateError
	default:
		return StatePartial
	}
}

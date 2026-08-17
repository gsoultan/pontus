package protocol

import "testing"

func frame(tag byte, body []byte) []byte {
	out := []byte{tag}
	n := uint32(len(body) + 4)
	out = append(out, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	return append(out, body...)
}

func cstring(s string) []byte { return append([]byte(s), 0) }

// A Sync means ReadyForQuery, whatever else the batch contains.
func TestResponseEndForSyncWaitsForReadyForQuery(t *testing.T) {
	p := &PostgresHandler{}

	batch := frame('P', append(cstring("s1"), cstring("SELECT 1")...))
	batch = append(batch, frame('B', cstring(""))...)
	batch = append(batch, frame('S', nil)...)

	end := p.ResponseEndFor(batch)
	if !end.OnReadyForQuery {
		t.Error("a batch containing Sync must wait for ReadyForQuery")
	}
}

// A simple query is answered with ReadyForQuery too.
func TestResponseEndForSimpleQuery(t *testing.T) {
	p := &PostgresHandler{}
	if end := p.ResponseEndFor(frame('Q', cstring("SELECT 1"))); !end.OnReadyForQuery {
		t.Error("a simple query must wait for ReadyForQuery")
	}
}

// Flush produces no ReadyForQuery, so the reply ends at the answer to the last
// message that produces one. Waiting for ReadyForQuery here blocks forever,
// which is what hung asyncpg.
func TestResponseEndForFlushEndsAtTheLastReply(t *testing.T) {
	p := &PostgresHandler{}

	for name, tc := range map[string]struct {
		batch []byte
		want  byte
	}{
		"parse then flush": {
			batch: append(frame('P', append(cstring("s1"), cstring("SELECT 1")...)),
				frame('H', nil)...),
			want: '1', // ParseComplete
		},
		"bind then flush": {
			batch: append(frame('B', cstring("")), frame('H', nil)...),
			want:  '2', // BindComplete
		},
		"close then flush": {
			batch: append(frame('C', append([]byte{'S'}, cstring("s1")...)), frame('H', nil)...),
			want:  '3', // CloseComplete
		},
		"execute then flush": {
			batch: append(frame('E', append(cstring(""), 0, 0, 0, 0)), frame('H', nil)...),
			want:  'C', // CommandComplete
		},
	} {
		t.Run(name, func(t *testing.T) {
			end := p.ResponseEndFor(tc.batch)
			if end.OnReadyForQuery {
				t.Fatal("a Flush-terminated batch must not wait for ReadyForQuery")
			}
			if !end.awaited(tc.want) {
				t.Errorf("terminators %q do not include %q", end.Tags, string(tc.want))
			}
		})
	}
}

// A describe is answered with a row description or, for a statement with no
// result, NoData.
func TestResponseEndForDescribe(t *testing.T) {
	p := &PostgresHandler{}
	batch := append(frame('D', append([]byte{'S'}, cstring("s1")...)), frame('H', nil)...)

	end := p.ResponseEndFor(batch)
	if !end.awaited('T') || !end.awaited('n') {
		t.Errorf("describe terminators = %q, want both T and n", end.Tags)
	}
}

// An ErrorResponse ends any exchange, whatever was asked for — otherwise a
// failed Parse leaves the reply looking unfinished.
func TestErrorResponseAlwaysEndsAReply(t *testing.T) {
	end := ResponseEnd{Tags: []byte{'1'}}
	if !end.awaited('E') {
		t.Error("an ErrorResponse must end a reply")
	}
}

// The scanner has to survive a message split across reads, because the socket
// decides where chunks land, not the protocol.
func TestReplyScannerHandlesSplitMessages(t *testing.T) {
	reply := frame('1', nil)                  // ParseComplete
	reply = append(reply, frame('t', nil)...) // ParameterDescription
	reply = append(reply, frame('T', []byte{0, 0})...)

	scan := newReplyScanner(ResponseEnd{Tags: []byte{'T'}})

	// One byte at a time is the worst case a socket can produce.
	var done bool
	for i := range reply {
		var d bool
		d, _ = scan.feed(reply[i : i+1])
		if d {
			done = true
		}
	}
	if !done {
		t.Error("the scanner never saw the terminator when fed one byte at a time")
	}
}

// ReadyForQuery's transaction status has to be read even when it lands in a
// later chunk than its header.
func TestReplyScannerReadsTransactionStatusAcrossASplit(t *testing.T) {
	reply := frame('Z', []byte{'T'}) // in a transaction

	scan := newReplyScanner(ResponseEnd{OnReadyForQuery: true})

	done, state := scan.feed(reply[:3]) // header only, partially
	if done {
		t.Fatal("reported done before the message arrived")
	}
	done, state = scan.feed(reply[3:])
	if !done {
		t.Fatal("did not report done after ReadyForQuery")
	}
	if state != StateInTransaction {
		t.Errorf("state = %v, want InTransaction", state)
	}
}

func TestReplyScannerReportsIdle(t *testing.T) {
	scan := newReplyScanner(ResponseEnd{OnReadyForQuery: true})
	done, state := scan.feed(frame('Z', []byte{'I'}))
	if !done || state != StateIdle {
		t.Errorf("done=%v state=%v, want true/Idle", done, state)
	}
}

// A batch with nothing that produces a reply must not make the proxy wait.
func TestResponseEndForLoneFlushWaitsForNothing(t *testing.T) {
	p := &PostgresHandler{}
	end := p.ResponseEndFor(frame('H', nil))
	if end.OnReadyForQuery || len(end.Tags) != 0 {
		t.Errorf("a lone Flush should await nothing, got %+v", end)
	}
}

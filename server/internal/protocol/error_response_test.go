package protocol

import "testing"

func TestErrorResponseIsAWellFormedMessage(t *testing.T) {
	msg := ErrorResponse(SeverityFatal, SQLStateQueryCanceled, "too slow")
	if msg[0] != 'E' {
		t.Fatalf("tag = %q, want 'E'", msg[0])
	}
	// The length field covers everything after the tag, per the wire protocol.
	if got, want := int(msg[1])<<24|int(msg[2])<<16|int(msg[3])<<8|int(msg[4]), len(msg)-1; got != want {
		t.Fatalf("declared length %d, actual %d", got, want)
	}
	if msg[len(msg)-1] != 0 {
		t.Fatal("message is not terminated by a zero byte")
	}
	for _, want := range []string{"FATAL", "57014", "too slow"} {
		if !contains(msg, want) {
			t.Errorf("message does not carry %q", want)
		}
	}
	// The reply scanner must treat it as ending the reply, or a client handed
	// this instead of a result set waits forever for a terminator.
	scanner := &replyScanner{end: ResponseEnd{Tags: []byte{'C'}}}
	if done, _ := scanner.feed(msg); !done {
		t.Error("the reply scanner does not see an ErrorResponse as ending the reply")
	}
}

func contains(hay []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

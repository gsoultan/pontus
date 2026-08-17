package proxy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/pkg/config"
)

// A reply is streamed to the client but held here while it is captured, so an
// unbounded capture buffers a whole result set in the proxy's heap — per
// concurrent client — on the way to a cache that would never keep something
// that size.
func TestCaptureStopsAtItsBound(t *testing.T) {
	c := newResponseCapture(new(bytes.Buffer), 1024)

	c.Write([]byte(strings.Repeat("a", 512)))
	if c.overflowed {
		t.Fatal("gave up below the bound")
	}
	if got := len(c.Bytes()); got != 512 {
		t.Fatalf("captured %d bytes, want 512", got)
	}

	c.Write([]byte(strings.Repeat("b", 1024)))
	if !c.overflowed {
		t.Fatal("captured past the bound")
	}

	// The buffer must be released, not merely abandoned: holding megabytes for
	// the life of a session that will never read them is the same leak, smaller.
	if c.buf.Len() != 0 {
		t.Errorf("abandoned capture still holds %d bytes", c.buf.Len())
	}
	if c.Bytes() != nil {
		t.Error("an abandoned capture returned bytes; they are a truncated reply")
	}
	// Later chunks must not restart it.
	c.Write([]byte("c"))
	if c.buf.Len() != 0 {
		t.Error("capture resumed after overflowing")
	}
}

// Collapsed followers write the leader's captured bytes straight to their
// client. Handed a truncated buffer, a follower emits a partial reply and
// leaves its client waiting for the rest of a message that never comes — so an
// overflow has to read as a failure, which makes followers run the query
// themselves.
func TestOverflowIsReportedAsAFailureToFollowers(t *testing.T) {
	c := newResponseCapture(new(bytes.Buffer), 16)

	if c.Err() != nil {
		t.Fatal("a capture within its bound reported an error")
	}

	c.Write([]byte(strings.Repeat("x", 64)))

	if c.Err() == nil {
		t.Fatal("an overflowed capture reported success; followers would be " +
			"handed an empty reply and hang")
	}
}

// An unset bound must not mean "no bound".
func TestZeroBoundFallsBackToTheDefault(t *testing.T) {
	if got := newResponseCapture(new(bytes.Buffer), 0).limit; got != defaultMaxCaptureBytes {
		t.Errorf("limit = %d with no configured bound, want %d", got, defaultMaxCaptureBytes)
	}
}

// An unset config block is a nil pointer, not a zero struct.
//
// `cache:` omitted leaves config.Options.Cache nil, and reconfigure reads
// several such blocks. Dereferencing one without a check faults on the most
// ordinary configuration there is — a minimal config file, and every test that
// builds a Gateway from &config.Options{}.
func TestGatewayBuildsFromAnEmptyConfig(t *testing.T) {
	for name, cfg := range map[string]*config.Options{
		"entirely empty": {},
		"cache block present but off": {
			Cache: &config.Cache{Enabled: false},
		},
		"cache block with a bound": {
			Cache: &config.Cache{Enabled: true, MaxEntrySize: 4096},
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewGateway(&mockHandler{}, &mockBalancer{}, nil, cfg, nil)
			defer g.cancel()

			if g.maxCaptureBytes <= 0 {
				t.Errorf("capture bound is %d; an unbounded capture buffers whole "+
					"result sets in the proxy's heap", g.maxCaptureBytes)
			}
		})
	}
}

// A configured bound must actually reach the gateway.
func TestConfiguredCaptureBoundIsApplied(t *testing.T) {
	g := NewGateway(&mockHandler{}, &mockBalancer{}, nil,
		&config.Options{Cache: &config.Cache{MaxEntrySize: 4096}}, nil)
	defer g.cancel()

	if g.maxCaptureBytes != 4096 {
		t.Errorf("capture bound = %d, want the configured 4096", g.maxCaptureBytes)
	}
}

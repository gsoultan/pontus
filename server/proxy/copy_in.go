package proxy

import (
	"net"
	"sync/atomic"
	"time"

	"github.com/gsoultan/pontus/pkg/buffer"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// COPY ... FROM STDIN turns the exchange around.
//
// Every other statement is request-then-reply, which is why the proxy can
// forward a request and then read the backend until the reply ends. COPY is
// not: the backend answers with CopyInResponse and then stops talking, because
// it is waiting for the client. A proxy sitting on the backend socket has
// nothing to read, and the data it is waiting for is on the other side of it.
//
// Every \copy, every pg_restore, every bulk loader hung there until
// query_timeout killed the session.

// copyPump relays client messages to the backend for the duration of a COPY.
//
// It runs alongside the backend reader rather than in place of it. A sequential
// pump deadlocks on the ordinary failure: the backend rejects a row, sends
// ErrorResponse and stops reading, the client keeps sending, the backend's
// receive buffer fills, and the write blocks with the ErrorResponse sitting
// unread on the other socket.
type copyPump struct {
	done chan struct{}
	err  error

	// stopping is set before the pump is interrupted, so a read cut short by
	// that interruption cannot forward what it had half-read. Those bytes
	// belong to a COPY the backend has already finished with; injecting them
	// would put them at the front of the next statement.
	stopping atomic.Bool
}

// startCopyPump begins forwarding the client's COPY data.
func (g *Gateway) startCopyPump(client, server net.Conn) *copyPump {
	pump := &copyPump{done: make(chan struct{})}

	go func() {
		defer close(pump.done)
		pump.err = pump.relay(g, client, server)
	}()

	return pump
}

// stop ends the pump and waits for it, unblocking a read that is still waiting
// on a client which has stopped sending.
//
// Interrupting with a deadline rather than closing the connection: the session
// continues after the COPY, so the socket has to survive.
func (p *copyPump) stop(client net.Conn) error {
	select {
	case <-p.done:
		return p.err
	default:
	}

	p.stopping.Store(true)
	_ = client.SetReadDeadline(time.Now())
	<-p.done
	_ = client.SetReadDeadline(time.Time{})

	// A pump interrupted on purpose did not fail.
	return nil
}

// relayCopyData forwards whole client messages until the COPY ends.
//
// Whole messages, because it has to recognise the one that ends the phase: if
// it read past CopyDone it would swallow the beginning of the next statement,
// and if it stopped short of it the backend would never be told the data was
// finished.
func (p *copyPump) relay(g *Gateway, client, server net.Conn) error {
	buf := buffer.Get()
	defer buffer.Put(buf)

	for {
		data, err := g.readRequest(client, buf)
		if p.stopping.Load() {
			return nil
		}
		if len(data) > 0 {
			if _, werr := server.Write(data); werr != nil {
				return werr
			}
			if endsCopyIn(data) {
				return nil
			}
		}
		if err != nil {
			return err
		}
	}
}

// endsCopyIn reports whether these messages finish the client's side of a COPY.
//
// CopyDone ('c') completes it; CopyFail ('f') abandons it. Both are answered by
// the backend, so the reader resumes either way.
func endsCopyIn(data []byte) bool {
	framer := protocol.PostgresFramer{}
	for len(data) > 0 {
		total, ok := framer.MessageLength(data)
		if !ok || total > len(data) {
			return false
		}
		if data[0] == 'c' || data[0] == 'f' {
			return true
		}
		data = data[total:]
	}
	return false
}

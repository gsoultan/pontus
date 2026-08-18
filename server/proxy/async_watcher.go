package proxy

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/gsoultan/pontus/pkg/buffer"
)

// Delivering messages the client never asked for.
//
// LISTEN/NOTIFY is the one part of the protocol where the backend speaks
// without being spoken to. A notification is sent when the notifying
// transaction commits, which is almost always while the listening session is
// sitting idle — and idle is exactly when a request/reply proxy is blocked
// reading its *client*, not its backend. The notification sat in the socket
// until the client happened to run another query, which for a listener may be
// never.
//
// So while an idle session is waiting for its next statement, something has to
// be watching the backend.

// asyncMessageTimeout bounds finishing a message already in flight. Reaching it
// means the backend stopped mid-message, which is a broken connection rather
// than a quiet one.
const asyncMessageTimeout = 30 * time.Second

// asyncPollInterval is how long the watcher waits for a message before checking
// whether it has been asked to stop.
//
// Polling rather than being interrupted from outside. The first version had
// stop() set a read deadline on the same socket the watcher was managing
// deadlines on, and the two raced: the watcher's own
// `defer SetReadDeadline(time.Time{})` could clear the deadline stop() had just
// set, after which the watcher blocked forever and stop() waited on it forever.
// One owner for the deadline removes the race outright, at the cost of one
// wakeup per interval on an idle listening session.
const asyncPollInterval = 50 * time.Millisecond

// asyncTags are the messages a backend may send unprompted: NotificationResponse,
// NoticeResponse and ParameterStatus. Anything else arriving while no statement
// is outstanding means the stream is out of step, and forwarding it would pass
// that confusion to the client.
var asyncTags = [...]byte{'A', 'N', 'S'}

// asyncWatcher forwards unprompted backend messages to the client while the
// session waits for its next statement.
type asyncWatcher struct {
	done     chan struct{}
	stopping atomic.Bool
}

// watchAsync starts forwarding notifications until stopped.
func (g *Gateway) watchAsync(client, server net.Conn) *asyncWatcher {
	w := &asyncWatcher{done: make(chan struct{})}

	go func() {
		defer close(w.done)
		if err := w.run(client, server); err != nil && !w.stopping.Load() {
			slog.Debug("Async watcher stopped", "error", err)
		}
	}()

	return w
}

// stop ends the watch before the session sends its next statement.
//
// Nothing here touches the connection. The watcher notices the flag within
// asyncPollInterval and leaves between messages, which is the only point at
// which leaving is safe; the deadline it was using is cleared once it has
// actually gone.
func (w *asyncWatcher) stop(server net.Conn) {
	w.stopping.Store(true)
	<-w.done
	_ = server.SetReadDeadline(time.Time{})
}

// run reads the backend a whole message at a time.
//
// Whole messages, and never abandoning one it has started. The interruption
// that stops this watcher lands on whichever read is in progress, so the only
// safe place to be interrupted is *between* messages — a watcher that dropped
// four bytes of a header would leave the next reader looking at the middle of
// one.
func (w *asyncWatcher) run(client, server net.Conn) error {
	buf := buffer.Get()
	defer buffer.Put(buf)

	var header [5]byte
	for {
		if w.stopping.Load() {
			return nil
		}

		// Wait a little for a message, then look up to see whether the session
		// wants its connection back.
		if err := server.SetReadDeadline(time.Now().Add(asyncPollInterval)); err != nil {
			return err
		}

		n, err := io.ReadFull(server, header[:])
		if n == 0 {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// Nothing arrived in this interval; that is the normal case.
				continue
			}
			return err
		}
		if err != nil {
			// Committed to this message. Finish it even while stopping, or the
			// bytes already taken are lost from the middle of the stream.
			if derr := server.SetReadDeadline(time.Now().Add(asyncMessageTimeout)); derr != nil {
				return derr
			}
			if _, rerr := io.ReadFull(server, header[n:]); rerr != nil {
				return rerr
			}
		}

		if !isAsyncTag(header[0]) {
			// Not something a backend sends unprompted. Stop watching and let
			// the session's own reader deal with it rather than guess.
			return errUnexpectedAsync
		}

		if err := w.forward(client, server, header, buf); err != nil {
			return err
		}
	}
}

// forward relays one whole message, body included.
func (w *asyncWatcher) forward(client, server net.Conn, header [5]byte, buf []byte) error {
	length := int(uint32(header[1])<<24 | uint32(header[2])<<16 |
		uint32(header[3])<<8 | uint32(header[4]))
	if length < 4 {
		return errUnexpectedAsync
	}

	// The watcher is the only thing that sets deadlines on this connection, so
	// there is nothing to restore here — the next loop sets its own, and stop()
	// clears it once the watcher has gone.
	if err := server.SetReadDeadline(time.Now().Add(asyncMessageTimeout)); err != nil {
		return err
	}

	body := buf[:0]
	if remaining := length - 4; remaining > 0 {
		if remaining > len(buf) {
			body = make([]byte, remaining)
		} else {
			body = buf[:remaining]
		}
		if _, err := io.ReadFull(server, body); err != nil {
			return err
		}
	}

	if _, err := client.Write(header[:]); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := client.Write(body); err != nil {
			return err
		}
	}
	return nil
}

func isAsyncTag(tag byte) bool {
	for _, want := range asyncTags {
		if tag == want {
			return true
		}
	}
	return false
}

// errUnexpectedAsync reports a message arriving while no statement was
// outstanding that a backend does not send unprompted.
var errUnexpectedAsync = errors.New("unexpected backend message while idle")

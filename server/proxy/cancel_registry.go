package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gsoultan/pontus/server/internal/protocol"
)

// Routing a cancel request.
//
// Cancellation is deliberately out-of-band: the client opens a *second*
// connection, sends the process id and secret it was given at startup, and gets
// no reply. Nothing in that packet says which server the process is on, and a
// pooler sits in front of several.
//
// Pontus forwards each backend's own BackendKeyData to the client rather than
// inventing one, so the values are real and the backend will honour them. What
// is missing is only the routing, which is what this remembers.

// cancelDialTimeout bounds the side connection opened to deliver a cancel.
//
// A cancel is best-effort by nature — the query may finish on its own first —
// so this fails fast rather than holding a goroutine on an unreachable backend.
const cancelDialTimeout = 5 * time.Second

// cancelRegistry maps a backend process id to the address it lives on.
//
// Bounded by the number of live sessions, not by anything a client sends: an
// entry is added when a session's backend answers with BackendKeyData and
// removed when that session ends.
type cancelRegistry struct {
	mu    sync.RWMutex
	byPID map[uint32]string
}

func newCancelRegistry() *cancelRegistry {
	return &cancelRegistry{byPID: make(map[uint32]string)}
}

// remember records where a session's backend process is.
func (r *cancelRegistry) remember(backendKey []byte, addr string) uint32 {
	pid, ok := protocol.BackendKeyProcessID(backendKey)
	if !ok || addr == "" {
		return 0
	}

	r.mu.Lock()
	r.byPID[pid] = addr
	r.mu.Unlock()
	return pid
}

// forget drops a session's entry.
func (r *cancelRegistry) forget(pid uint32) {
	if pid == 0 {
		return
	}
	r.mu.Lock()
	delete(r.byPID, pid)
	r.mu.Unlock()
}

// addrFor returns where a process id was last seen.
func (r *cancelRegistry) addrFor(pid uint32) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	addr, ok := r.byPID[pid]
	return addr, ok
}

// routeCancel delivers a cancel request to the backend running the query.
//
// The packet is forwarded byte for byte. Its secret is the only authorisation
// there is and it is the backend's to check — Pontus decides *where* the
// request goes, never whether it is allowed. An unknown process id is dropped
// silently: answering differently would tell an unauthenticated caller which
// process ids exist.
func (g *Gateway) routeCancel(key *protocol.CancelKey) error {
	if key == nil {
		return fmt.Errorf("no cancel key")
	}

	addr, ok := g.cancels.addrFor(key.ProcessID)
	if !ok {
		slog.Debug("Cancel request for an unknown backend process", "pid", key.ProcessID)
		return nil
	}

	conn, err := net.DialTimeout("tcp", addr, cancelDialTimeout)
	if err != nil {
		return fmt.Errorf("cancel: dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetWriteDeadline(time.Now().Add(cancelDialTimeout)); err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	if _, err := conn.Write(key.Raw); err != nil {
		return fmt.Errorf("cancel: send to %s: %w", addr, err)
	}

	// A backend answers a cancel by closing, never by replying.
	slog.Debug("Cancel request forwarded", "pid", key.ProcessID, "backend", addr)
	return nil
}

// backendAddr reports where a backend listens, or "" if it cannot say.
func backendAddr(b interface{ Address() string }) string {
	if b == nil {
		return ""
	}
	return b.Address()
}

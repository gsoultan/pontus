package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
	"github.com/gsoultan/pontus/server/internal/replication"
)

// StreamRegistry is the subset of the replication registry the gateway needs.
// Declared here so the data plane does not depend on the control plane to
// carry a stream.
type StreamRegistry interface {
	Add(s *replication.Stream) bool
	Remove(id string)
}

// streamContext returns a context every live replication stream derives from,
// creating one on first use.
//
// Cancelling it is how failover ends every stream at once: a CopyBoth feed has
// no idle moment to check a flag in, so the only way to stop one is to tear
// down its connection.
func (g *Gateway) streamContext() context.Context {
	g.streamCtxMu.Lock()
	defer g.streamCtxMu.Unlock()

	if g.streamCtx == nil {
		g.streamCtx, g.streamCancel = context.WithCancel(context.Background())
	}
	return g.streamCtx
}

// terminateStreams ends every live replication stream.
//
// Called on failover, and deliberately not followed by a reconnect. Logical
// replication slots are not copied to standbys, so the promoted node has no
// slot for these consumers: resuming them there would restart from a position
// the consumer never reached and silently lose or repeat changes. Ending the
// stream makes the consumer fall back to its own checkpoint, which is the only
// place the correct position is known.
func (g *Gateway) terminateStreams(reason string) {
	g.streamCtxMu.Lock()
	cancel := g.streamCancel
	g.streamCtx, g.streamCancel = nil, nil
	g.streamCtxMu.Unlock()

	if cancel == nil {
		return
	}
	slog.Warn("Terminating replication streams", "reason", reason)
	cancel()
}

// SetStreamRegistry attaches the registry that tracks live CDC consumers.
// Without one the gateway refuses replication rather than carrying an
// unaccounted stream.
func (g *Gateway) SetStreamRegistry(r StreamRegistry) {
	g.streams.Store(&r)
}

// handleReplication carries a CDC stream for the life of the connection.
//
// Two things make this different from a pooled session, and both are the
// reason it cannot reuse the transaction loop:
//
//   - It is pinned. A replication slot lives on the node that created it, so
//     the balancer is bypassed entirely and the primary is resolved directly.
//     Balancing a slot is meaningless.
//   - It is held. The connection is occupied for hours by a single CopyBoth
//     feed, so it never returns to the idle set and is destroyed on exit.
//
// The connection still comes from the pool. Dialing around it would break the
// one guarantee the pool exists to provide: that Pontus does not open more
// connections than the database was told to expect.
func (g *Gateway) handleReplication(ctx context.Context, client net.Conn, state *protocol.SessionState, remoteAddr string) {
	// Derive from the shared stream context so a failover ends this stream
	// along with every other one.
	ctx, cancel := context.WithCancel(joinCancel(ctx, g.streamContext()))
	defer cancel()

	registry := g.streamRegistry()
	if registry == nil {
		g.refuseReplication(client, remoteAddr,
			"replication streams are not enabled on this proxy")
		return
	}

	// Resolve the primary directly. Hint carries ReadOnly=false, but the
	// balancer may still pick a replica under some strategies, so the role is
	// checked rather than assumed.
	backend, server, err := g.acquireBackend(ctx, balancer2.Hint{
		ReadOnly:   false,
		CallerZone: g.current().localZone,
		Key:        remoteAddr,
	})
	if err != nil {
		g.refuseReplication(client, remoteAddr,
			fmt.Sprintf("no backend available for replication: %v", err))
		return
	}

	if backend.Role() != pool2.RolePrimary {
		backend.Release(server)
		g.refuseReplication(client, remoteAddr,
			"logical replication is only served by the primary")
		return
	}

	stream := &replication.Stream{
		ID:          fmt.Sprintf("%s-%d", remoteAddr, time.Now().UnixNano()),
		ClientAddr:  remoteAddr,
		BackendAddr: backend.Address(),
		Database:    state.Database,
		User:        state.User,
		Kind:        replicationKind(state.Replication),
		StartedAt:   time.Now(),
	}

	// The budget is checked before the startup packet is forwarded, so a
	// refused consumer never reaches the database at all.
	if !registry.Add(stream) {
		backend.Release(server)
		g.refuseReplication(client, remoteAddr,
			"replication budget is full; raise max stream connections or disconnect an idle consumer")
		return
	}

	defer func() {
		registry.Remove(stream.ID)
		// Never recycle: this connection spent its life in replication mode and
		// cannot serve a pooled session afterwards.
		if c, ok := server.(interface{ MarkBroken() }); ok {
			c.MarkBroken()
		}
		backend.Release(server)
	}()

	if err := g.handler.StartReplication(ctx, client, server, state); err != nil {
		slog.Warn("Replication handshake failed",
			"client", remoteAddr, "backend", backend.Address(), "error", err)
		return
	}

	slog.Info("Replication stream started",
		"client", remoteAddr, "backend", backend.Address(),
		"user", state.User, "database", state.Database, "kind", stream.Kind)

	pipe(ctx, client, server)

	slog.Info("Replication stream ended",
		"client", remoteAddr, "backend", backend.Address(),
		"duration", time.Since(stream.StartedAt).Round(time.Second))
}

// pipe copies in both directions until either side closes.
//
// A replication feed is not request/response: the backend streams WAL while the
// client streams standby status updates, both unprompted. Neither direction can
// be driven by the other, so each gets its own goroutine and the first to end
// tears down the pair.
func pipe(ctx context.Context, client, server net.Conn) {
	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(server, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, server)
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}

	// Closing both ends unblocks the surviving copy; without it that goroutine
	// would live until the peer noticed, which for an idle stream is never.
	_ = client.SetDeadline(time.Now())
	_ = server.SetDeadline(time.Now())
}

// refuseReplication tells the client why, in a form its driver will surface.
func (g *Gateway) refuseReplication(client net.Conn, remoteAddr, reason string) {
	slog.Warn("Refused replication stream", "client", remoteAddr, "reason", reason)
	if err := protocol.WritePostgresError(client, "0A000", reason); err != nil {
		slog.Debug("Failed to report replication refusal", "client", remoteAddr, "error", err)
	}
}

func (g *Gateway) streamRegistry() StreamRegistry {
	if r := g.streams.Load(); r != nil {
		return *r
	}
	return nil
}

// replicationKind maps the startup parameter to the stream type.
// "database" selects logical decoding; anything else truthy is physical.
func replicationKind(value string) string {
	if value == "database" {
		return "logical"
	}
	return "physical"
}

// joinCancel returns a context cancelled when either parent is.
//
// A stream ends when its own connection context ends, and also when failover
// cancels every stream at once; neither can be expressed as a parent of the
// other.
func joinCancel(a, b context.Context) context.Context {
	ctx, cancel := context.WithCancel(a)
	go func() {
		select {
		case <-b.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}

package handler

import (
	"context"
	"crypto/sha256"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Bounds on the server-side push cadence.
//
// The floor exists because interval_ms comes from the client: without it a
// dashboard could ask the server to rebuild the full status payload in a busy
// loop, which on a small host competes directly with the data plane.
const (
	statusStreamDefaultInterval = 2 * time.Second
	statusStreamMinInterval     = 500 * time.Millisecond
	statusStreamMaxInterval     = 30 * time.Second

	// statusStreamKeepalive forces a send even when nothing changed, so an
	// idle stream still proves itself alive to proxies and to the client.
	statusStreamKeepalive = 20 * time.Second
)

// StreamStatus pushes the status payload for as long as the client is connected.
//
// This replaces polling GetStatus on a timer. The dashboard re-fetched every
// backend, twenty top queries with their full SQL text, the topology and the
// system metrics on a fixed interval, per connected dashboard, whether or not
// anything had changed. Here the server decides when there is something worth
// sending: identical payloads are suppressed until the keepalive falls due.
func (h *managementHandler) StreamStatus(
	ctx context.Context,
	req *connect.Request[endpoints.StreamStatusRequest],
	stream *connect.ServerStream[endpoints.GetStatusResponse],
) error {
	interval := clampStatusInterval(req.Msg.GetIntervalMs())

	statusReq := &endpoints.GetStatusRequest{
		ProjectId: req.Msg.GetProjectId(),
		ProxyId:   req.Msg.GetProxyId(),
	}

	// Deterministic marshalling so map ordering does not make an unchanged
	// payload look different every tick.
	marshal := proto.MarshalOptions{Deterministic: true}

	var lastDigest [sha256.Size]byte
	var lastSent time.Time

	send := func() error {
		resp, err := h.endpoints.GetStatusEndpoint(ctx, statusReq)
		if err != nil {
			return err
		}
		status, ok := resp.(*endpoints.GetStatusResponse)
		if !ok || status == nil {
			return nil
		}

		digest := lastDigest
		if raw, err := marshal.Marshal(status); err == nil {
			digest = sha256.Sum256(raw)
			if digest == lastDigest && time.Since(lastSent) < statusStreamKeepalive {
				return nil
			}
		}

		if err := stream.Send(status); err != nil {
			return err
		}
		lastDigest, lastSent = digest, time.Now()
		return nil
	}

	// Send once immediately so the dashboard paints without waiting a tick.
	if err := send(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected or the server is shutting down. Returning nil
			// keeps a normal disconnect out of the error logs.
			return nil
		case <-ticker.C:
			if err := send(); err != nil {
				return err
			}
		}
	}
}

func clampStatusInterval(ms int32) time.Duration {
	if ms <= 0 {
		return statusStreamDefaultInterval
	}
	interval := time.Duration(ms) * time.Millisecond
	if interval < statusStreamMinInterval {
		return statusStreamMinInterval
	}
	if interval > statusStreamMaxInterval {
		return statusStreamMaxInterval
	}
	return interval
}

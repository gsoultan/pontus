package handler

import (
	"context"

	"connectrpc.com/connect"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// ListReplicationStreams returns the CDC consumers attached to a proxy.
func (h *managementHandler) ListReplicationStreams(
	ctx context.Context,
	req *connect.Request[endpoints.ListReplicationStreamsRequest],
) (*connect.Response[endpoints.ListReplicationStreamsResponse], error) {
	resp, err := h.endpoints.ListReplicationStreamsEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.ListReplicationStreamsResponse)), nil
}

// TerminateReplicationStream disconnects one consumer.
func (h *managementHandler) TerminateReplicationStream(
	ctx context.Context,
	req *connect.Request[endpoints.TerminateReplicationStreamRequest],
) (*connect.Response[endpoints.TerminateReplicationStreamResponse], error) {
	resp, err := h.endpoints.TerminateReplicationStreamEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.TerminateReplicationStreamResponse)), nil
}

// CreateLogicalSlot pre-creates a logical replication slot on a node.
func (h *managementHandler) CreateLogicalSlot(
	ctx context.Context,
	req *connect.Request[endpoints.CreateLogicalSlotRequest],
) (*connect.Response[endpoints.CreateLogicalSlotResponse], error) {
	resp, err := h.endpoints.CreateLogicalSlotEndpoint(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp.(*endpoints.CreateLogicalSlotResponse)), nil
}

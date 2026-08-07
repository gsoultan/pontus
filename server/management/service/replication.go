package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Replication exposes the live CDC/replication streams attached to a proxy.
type Replication interface {
	ListStreams(ctx context.Context, req *endpoints.ListReplicationStreamsRequest) (*endpoints.ListReplicationStreamsResponse, error)
	TerminateStream(ctx context.Context, req *endpoints.TerminateReplicationStreamRequest) (*endpoints.TerminateReplicationStreamResponse, error)
	CreateLogicalSlot(ctx context.Context, req *endpoints.CreateLogicalSlotRequest) (*endpoints.CreateLogicalSlotResponse, error)
}

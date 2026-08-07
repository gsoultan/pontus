package manager

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/internal/replication"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/infrastructure/state"
	"github.com/gsoultan/pontus/server/management/service"
)

// Replication serves the live stream view.
//
// Streams are reported from the in-memory registry the proxy owns, not from
// the database: a stream exists only while its connection does, so asking
// PostgreSQL would describe consumers this process is not serving.
type Replication struct {
	registry *registry.Registry
}

// NewReplication creates the replication manager.
func NewReplication(r *registry.Registry) service.Replication {
	return &Replication{registry: r}
}

func (m *Replication) proxy(projectID, proxyID string) (*state.Proxy, error) {
	p, err := m.registry.GetProjectState(projectID)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	if proxyID != "" {
		if ps, ok := p.Proxies[proxyID]; ok {
			return ps, nil
		}
		return nil, status.Errorf(codes.NotFound, "proxy %s not found", proxyID)
	}
	for _, ps := range p.Proxies {
		return ps, nil
	}
	return nil, status.Error(codes.NotFound, "no proxy configured")
}

func (m *Replication) ListStreams(_ context.Context, req *endpoints.ListReplicationStreamsRequest) (*endpoints.ListReplicationStreamsResponse, error) {
	ps, err := m.proxy(req.ProjectId, req.ProxyId)
	if err != nil {
		return nil, err
	}

	reg := ps.Streams
	if reg == nil {
		// No registry yet means the proxy predates stream support; an empty
		// list is the truthful answer, not an error.
		return &endpoints.ListReplicationStreamsResponse{}, nil
	}

	live := reg.List()
	out := make([]*domain.ReplicationStream, 0, len(live))
	for _, s := range live {
		out = append(out, toProto(s))
	}

	return &endpoints.ListReplicationStreamsResponse{
		Streams: out,
		Used:    int32(len(live)),
		Budget:  int32(reg.Budget()),
	}, nil
}

func (m *Replication) TerminateStream(_ context.Context, req *endpoints.TerminateReplicationStreamRequest) (*endpoints.TerminateReplicationStreamResponse, error) {
	ps, err := m.proxy(req.ProjectId, req.ProxyId)
	if err != nil {
		return nil, err
	}
	if ps.Streams == nil {
		return nil, status.Error(codes.FailedPrecondition, "replication streams are not enabled on this proxy")
	}

	ps.Streams.Remove(req.StreamId)
	return &endpoints.TerminateReplicationStreamResponse{
		Success: true,
		Message: fmt.Sprintf("stream %s terminated; the consumer must resync from its own checkpoint", req.StreamId),
	}, nil
}

func (m *Replication) CreateLogicalSlot(_ context.Context, req *endpoints.CreateLogicalSlotRequest) (*endpoints.CreateLogicalSlotResponse, error) {
	if req.SlotName == "" {
		return nil, status.Error(codes.InvalidArgument, "slot name is required")
	}
	if _, err := m.proxy(req.ProjectId, req.ProxyId); err != nil {
		return nil, err
	}

	// Creating the slot on the node needs the CDC data path, which does not
	// exist yet. Failing explicitly is better than reporting success for a
	// slot no consumer will find.
	return nil, status.Error(codes.Unimplemented,
		"logical slot creation requires the CDC data path, which is not implemented yet")
}

func toProto(s replication.Stream) *domain.ReplicationStream {
	out := &domain.ReplicationStream{
		Id:           s.ID,
		SlotName:     s.SlotName,
		ClientAddr:   s.ClientAddr,
		BackendAddr:  s.BackendAddr,
		Database:     s.Database,
		User:         s.User,
		Kind:         s.Kind,
		Plugin:       s.Plugin,
		LagBytes:     s.LagBytes,
		LagMs:        s.LagMs,
		ConfirmedLsn: s.ConfirmedLSN,
		Active:       true,
	}
	if !s.StartedAt.IsZero() {
		out.StartedAt = timestamppb.New(s.StartedAt)
	}
	return out
}

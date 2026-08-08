package manager

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
	protocol2 "github.com/gsoultan/pontus/server/internal/protocol"
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

func (m *Replication) ListStreams(ctx context.Context, req *endpoints.ListReplicationStreamsRequest) (*endpoints.ListReplicationStreamsResponse, error) {
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
		Slots:   m.slots(ctx, ps),
		Used:    int32(len(live)),
		Budget:  int32(reg.Budget()),
	}, nil
}

// slots reads the slot inventory from the primary.
//
// Best effort: a proxy with no reachable primary still has streams worth
// showing, and failing the whole call because the inventory could not be read
// would hide them.
func (m *Replication) slots(ctx context.Context, ps *state.Proxy) []*domain.ReplicationSlot {
	backend := primaryOf(ps, "")
	if backend == nil {
		return nil
	}

	handler, ok := ps.Gateway.Handler().(*protocol2.PostgresHandler)
	if !ok {
		return nil
	}

	conn, err := backend.Acquire(ctx)
	if err != nil {
		slog.Debug("Slot inventory unavailable", "backend", backend.Address(), "error", err)
		return nil
	}
	defer backend.Release(conn)

	stats, err := handler.QuerySlotStats(ctx, conn)
	if err != nil {
		// Same constraint as slot creation: without credentials of its own,
		// Pontus can only run this on a connection some client already
		// authenticated. Best effort — the streams themselves are still worth
		// showing, and an empty inventory is not a reason to fail the call.
		slog.Debug("Slot inventory unavailable; Pontus has no administrative session",
			"backend", backend.Address(), "error", err)
		return nil
	}

	out := make([]*domain.ReplicationSlot, 0, len(stats))
	for _, st := range stats {
		out = append(out, &domain.ReplicationSlot{
			Name:          st.Name,
			Plugin:        st.Plugin,
			SlotType:      st.SlotType,
			Active:        st.Active,
			Database:      st.Database,
			ConfirmedLsn:  st.ConfirmedLSN,
			RetainedBytes: st.RetainedBytes,
		})
	}
	return out
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

func (m *Replication) CreateLogicalSlot(ctx context.Context, req *endpoints.CreateLogicalSlotRequest) (*endpoints.CreateLogicalSlotResponse, error) {
	if req.SlotName == "" {
		return nil, status.Error(codes.InvalidArgument, "slot name is required")
	}
	if _, err := m.proxy(req.ProjectId, req.ProxyId); err != nil {
		return nil, err
	}

	ps, err := m.proxy(req.ProjectId, req.ProxyId)
	if err != nil {
		return nil, err
	}

	// A logical slot decodes through a plugin and can only be created on the
	// node that will serve the stream, which is the primary.
	backend := primaryOf(ps, req.Address)
	if backend == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"logical replication slots can only be created on a healthy primary")
	}

	plugin := req.Plugin
	if plugin == "" {
		plugin = "pgoutput" // in-core, present on every supported version
	}

	handler, ok := ps.Gateway.Handler().(*protocol2.PostgresHandler)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition,
			"logical replication slots are only supported for PostgreSQL")
	}

	conn, err := backend.Acquire(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "acquire connection to %s: %v", backend.Address(), err)
	}
	defer backend.Release(conn)

	if err := handler.CreateLogicalReplicationSlot(ctx, conn, req.SlotName, plugin); err != nil {
		// Pontus forwards the client's startup packet rather than
		// authenticating to the backend itself, so a pooled connection is a raw
		// socket until some client has handshaked it. An administrative
		// statement has no session of its own to run in, and the backend closes
		// the connection — which surfaces as EOF.
		//
		// Making this legible rather than leaving an operator to decode "EOF"
		// against a database that is plainly up.
		return nil, status.Errorf(codes.FailedPrecondition,
			"could not create slot %s on %s: %v — Pontus has no credentials of its own for "+
				"administrative statements; create the slot directly on the node with "+
				"SELECT pg_create_logical_replication_slot('%s', '%s')",
			req.SlotName, backend.Address(), err, req.SlotName, plugin)
	}

	return &endpoints.CreateLogicalSlotResponse{
		Success: true,
		Message: fmt.Sprintf("slot %s created on %s with plugin %s", req.SlotName, backend.Address(), plugin),
		// The consumer connects to the proxy, not to the node — the whole point
		// is that it does not need to know which node holds the slot.
		ConsumerDsn: fmt.Sprintf("postgres://<user>@%s/<database>?replication=database",
			ps.Config.Address),
	}, nil
}

// primaryOf returns the healthy primary, optionally constrained to one address.
func primaryOf(ps *state.Proxy, address string) pool2.Backend {
	for _, b := range ps.Backends {
		if b.Role() != pool2.RolePrimary || !b.IsHealthy() {
			continue
		}
		if address == "" || b.Address() == address {
			return b
		}
	}
	return nil
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

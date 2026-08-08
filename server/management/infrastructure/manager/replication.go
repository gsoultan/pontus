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

	admin := backend.Admin()
	if !admin.Available() {
		// No admin_dsn configured: the streams are still worth showing, and an
		// empty inventory is not a reason to fail the call.
		return nil
	}

	rows, err := admin.Query(ctx, `SELECT slot_name, coalesce(plugin,''), slot_type, active,
		coalesce(database,''), coalesce(confirmed_flush_lsn::text,''),
		coalesce(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)::bigint, 0)
		FROM pg_replication_slots`)
	if err != nil {
		slog.Debug("Slot inventory query failed", "backend", backend.Address(), "error", err)
		return nil
	}
	defer rows.Close()

	type slotRow struct {
		name, plugin, slotType, database, lsn string
		active                                bool
		retained                              int64
	}

	var stats []slotRow
	for rows.Next() {
		var r slotRow
		if err := rows.Scan(&r.name, &r.plugin, &r.slotType, &r.active,
			&r.database, &r.lsn, &r.retained); err != nil {
			slog.Debug("Slot row scan failed", "backend", backend.Address(), "error", err)
			return nil
		}
		stats = append(stats, r)
	}

	out := make([]*domain.ReplicationSlot, 0, len(stats))
	for _, st := range stats {
		out = append(out, &domain.ReplicationSlot{
			Name:          st.name,
			Plugin:        st.plugin,
			SlotType:      st.slotType,
			Active:        st.active,
			Database:      st.database,
			ConfirmedLsn:  st.lsn,
			RetainedBytes: st.retained,
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

	admin := backend.Admin()
	if !admin.Available() {
		return nil, status.Errorf(codes.FailedPrecondition,
			"backend %s has no admin_dsn configured; Pontus needs a session of its own to create "+
				"a replication slot, because a client session carries the client's credentials",
			backend.Address())
	}

	// Validated as well as bound. These are values here, not identifiers, but
	// PostgreSQL rejects anything outside its slot-name rules anyway and a
	// local message is clearer than the server's.
	if err := protocol2.ValidateSlotName(req.SlotName); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := protocol2.ValidateOutputPlugin(plugin); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := admin.Exec(ctx,
		"SELECT pg_create_logical_replication_slot($1, $2)", req.SlotName, plugin); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"create slot %s on %s: %v", req.SlotName, backend.Address(), err)
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

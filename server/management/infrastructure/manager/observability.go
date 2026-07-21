package manager

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	observability2 "github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/pkg/observability/store"
	"github.com/gsoultan/pontus/server/internal/insights"
	"github.com/gsoultan/pontus/server/internal/orchestration"
	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/infrastructure/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Observability implements ObservabilityService.
type Observability struct {
	registry *registry.Registry
}

func NewObservability(registry *registry.Registry) *Observability {
	return &Observability{registry: registry}
}

func (m *Observability) GetStatus(ctx context.Context, req *endpoints.GetStatusRequest) (*endpoints.GetStatusResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	p.Mu.RLock()
	defer p.Mu.RUnlock()

	var ps *state.Proxy
	if req.ProxyId != "" {
		ps = p.Proxies[req.ProxyId]
	} else if len(p.Proxies) > 0 {
		for _, v := range p.Proxies {
			ps = v
			break
		}
	}

	if ps == nil {
		return nil, status.Error(codes.NotFound, "proxy not found")
	}

	var totalConns int64
	backendStatuses := make([]*domain.BackendStatus, 0, len(ps.Backends))

	for node := range slices.Values(ps.Backends) {
		conns := node.ActiveConns()
		totalConns += conns
		stats := node.Stats()

		status := &domain.BackendStatus{
			Address:            node.Address(),
			Zone:               node.Zone(),
			Healthy:            node.IsHealthy(),
			ActiveConns:        conns,
			LastCheck:          timestamppb.Now(),
			Role:               string(node.Role()),
			LatencyMs:          node.Latency().Milliseconds(),
			ReplicationLagMs:   node.ReplicationLag().Milliseconds(),
			IsDraining:         node.IsDraining(),
			Weight:             int32(node.Weight()),
			MaxConns:           stats.MaxConns,
			IdleConns:          stats.IdleConns,
			WaitQueueSize:      stats.WaitQueueSize,
			TotalRequests:      stats.TotalRequests,
			TotalErrors:        stats.TotalErrors,
			CurrentMaxConns:    stats.MaxConns,
			RttMs:              node.RTT().Milliseconds(),
			DbMetrics:          node.DatabaseMetrics(),
			InstalledVersion:   node.InstalledVersion(),
			RecommendedVersion: node.RecommendedVersion(),
		}

		// Fill in management info from config
		if ps.Config != nil {
			for _, bcfg := range ps.Config.Backends {
				if bcfg.Address == node.Address() {
					status.ManagedByAgent = bcfg.ManagedByAgent
					status.AgentAddress = bcfg.AgentAddress
					status.AgentConfig = bcfg.AgentConfig
					break
				}
			}
		}

		backendStatuses = append(backendStatuses, status)
	}

	totalRequests, totalErrors, rps := observability2.DefaultTracker.GlobalStats()
	rawMetrics := observability2.CollectSystemMetrics()

	var adaptiveStatus *domain.AdaptiveStatus
	var performanceSuggestions []*domain.PerformanceSuggestion

	// Get adaptive status from the first proxy if available (simplification)
	if ps != nil && len(ps.Backends) > 0 {
		for node := range slices.Values(ps.Backends) {
			// Try to get adaptive status from the first backend
			if srv, ok := node.(*pool.Server); ok {
				throttled, reason, maxWaiters, goroutines, suggestions := srv.AdaptiveStatus()
				adaptiveStatus = &domain.AdaptiveStatus{
					IsThrottled:       throttled,
					ThrottleReason:    reason,
					CurrentMaxWaiters: maxWaiters,
					ActiveGoroutines:  goroutines,
				}
				for _, sug := range suggestions {
					performanceSuggestions = append(performanceSuggestions, &domain.PerformanceSuggestion{
						Title:       sug.Title,
						Description: sug.Description,
						Level:       sug.Level,
						Action:      sug.Action,
					})
				}
				break
			}
		}
	}

	inFailover := false
	if ps.FailoverMgr != nil {
		s := ps.FailoverMgr.State()
		inFailover = s == orchestration.StatePromoting || s == orchestration.StateVerifying
	}

	topology := &domain.Topology{
		Nodes: []*domain.TopologyNode{
			{
				Id:      ps.Config.Id,
				Label:   ps.Config.Name,
				Type:    "proxy",
				Status:  "healthy",
				Address: ps.Config.Address,
			},
		},
		Edges: []*domain.TopologyEdge{},
	}

	for _, b := range backendStatuses {
		nodeID := fmt.Sprintf("backend-%s", b.Address)
		topology.Nodes = append(topology.Nodes, &domain.TopologyNode{
			Id:      nodeID,
			Label:   b.Address,
			Type:    b.Role,
			Status:  "healthy",
			Address: b.Address,
		})
		topology.Edges = append(topology.Edges, &domain.TopologyEdge{
			From: ps.Config.Id,
			To:   nodeID,
			Type: "traffic",
		})
	}

	return &endpoints.GetStatusResponse{
		Backends:          backendStatuses,
		TotalConns:        totalConns,
		Protocol:          p.Config.Protocol,
		BalancerType:      ps.Balancer.Name(),
		UptimeSeconds:     int64(observability2.DefaultTracker.Uptime().Seconds()),
		RequestsPerSecond: float32(rps),
		TotalRequests:     totalRequests,
		TotalErrors:       totalErrors,
		SystemMetrics: &domain.SystemMetrics{
			CpuUsagePercent:     float32(rawMetrics.CPUUsagePercent),
			MemoryTotalBytes:    rawMetrics.MemoryTotalBytes,
			MemoryUsedBytes:     rawMetrics.MemoryUsedBytes,
			MemoryUsagePercent:  float32(rawMetrics.MemoryUsagePercent),
			StorageTotalBytes:   rawMetrics.StorageTotalBytes,
			StorageUsedBytes:    rawMetrics.StorageUsedBytes,
			StorageUsagePercent: float32(rawMetrics.StorageUsagePercent),
			Goroutines:          uint64(rawMetrics.Goroutines),
		},
		InFailover:             inFailover,
		Topology:               topology,
		AdaptiveStatus:         adaptiveStatus,
		PerformanceSuggestions: performanceSuggestions,
	}, nil
}

func (m *Observability) StreamLogs(ctx context.Context, req *endpoints.StreamLogsRequest, logs chan<- *domain.LogEntry) error {
	ch := observability2.GlobalLogBroadcaster.Subscribe()
	defer observability2.GlobalLogBroadcaster.Unsubscribe(ch)
	defer close(logs)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case entry := <-ch:
			select {
			case logs <- entry:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (m *Observability) Explain(ctx context.Context, projectID string, query string) (string, error) {
	return m.registry.Explain(ctx, projectID, query)
}

func (m *Observability) ExplainQuery(ctx context.Context, req *endpoints.ExplainQueryRequest) (*endpoints.ExplainQueryResponse, error) {
	plan, err := m.Explain(ctx, req.ProjectId, req.Query)
	if err != nil {
		return nil, err
	}

	recommendations := m.analyzePlan(plan)

	return &endpoints.ExplainQueryResponse{
		Plan:            plan,
		Recommendations: recommendations,
	}, nil
}

func (m *Observability) analyzePlan(plan string) []*domain.Recommendation {
	var recs []*domain.Recommendation

	if strings.Contains(plan, "Seq Scan") {
		recs = append(recs, &domain.Recommendation{
			Type:    "Index Suggestion",
			Message: "Sequential scan detected. Consider adding an index on the filtered columns.",
			Impact:  "High",
		})
	}

	if strings.Contains(plan, "Parallel Seq Scan") {
		recs = append(recs, &domain.Recommendation{
			Type:    "Parallel Index Suggestion",
			Message: "Parallel sequential scan detected. Large table access could be optimized with an index.",
			Impact:  "Medium",
		})
	}

	if strings.Contains(plan, "Nested Loop") && strings.Contains(plan, "Join Filter") {
		recs = append(recs, &domain.Recommendation{
			Type:    "Join Optimization",
			Message: "Nested loop join with filter detected. Consider adding an index on the join columns to enable Hash Join or Merge Join.",
			Impact:  "High",
		})
	}

	return recs
}

func (m *Observability) GetLogs(ctx context.Context, req *endpoints.GetLogsRequest) (*endpoints.GetLogsResponse, error) {
	s := observability2.GlobalLogBroadcaster.GetStore()
	if s == nil {
		return nil, status.Error(codes.Unavailable, "log store not initialized")
	}

	filter := store.LogFilter{
		Level:  req.Level,
		Search: req.Search,
		Limit:  int(req.Limit),
		Offset: int(req.Offset),
	}

	if req.StartTime != nil {
		t := req.StartTime.AsTime()
		filter.StartTime = &t
	}
	if req.EndTime != nil {
		t := req.EndTime.AsTime()
		filter.EndTime = &t
	}

	if filter.Limit <= 0 {
		filter.Limit = 50
	}

	entries, total, err := s.GetLogs(ctx, filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get logs: %v", err)
	}

	return &endpoints.GetLogsResponse{
		Logs:       entries,
		TotalCount: int32(total),
	}, nil
}

func (m *Observability) GetMetricsHistory(ctx context.Context, req *endpoints.GetMetricsHistoryRequest) (*endpoints.GetMetricsHistoryResponse, error) {
	s := observability2.DefaultTracker.GetStore()
	if s == nil {
		return nil, status.Error(codes.Unavailable, "metric store not initialized")
	}

	start := req.StartTime.AsTime()
	end := req.EndTime.AsTime()
	if end.IsZero() {
		end = timestamppb.Now().AsTime()
	}

	history, err := s.GetHistory(ctx, start, end)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get metrics history: %v", err)
	}

	return &endpoints.GetMetricsHistoryResponse{
		History: history,
	}, nil
}

func (m *Observability) GetTopQueriesHistory(ctx context.Context, req *endpoints.GetTopQueriesHistoryRequest) (*endpoints.GetTopQueriesHistoryResponse, error) {
	s := observability2.DefaultTracker.GetStore()
	if s == nil {
		return nil, status.Error(codes.Unavailable, "metric store not initialized")
	}

	start := req.StartTime.AsTime()
	end := req.EndTime.AsTime()
	if end.IsZero() {
		end = timestamppb.Now().AsTime()
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}

	queries, err := s.GetTopQueries(ctx, start, end, limit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get top queries history: %v", err)
	}

	return &endpoints.GetTopQueriesHistoryResponse{
		TopQueries: queries,
	}, nil
}

func (m *Observability) TuneDatabase(ctx context.Context, req *endpoints.TuneDatabaseRequest) (*endpoints.TuneDatabaseResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	p.Mu.RLock()
	defer p.Mu.RUnlock()

	var ps *state.Proxy
	if req.ProxyId != "" {
		ps = p.Proxies[req.ProxyId]
	} else if len(p.Proxies) > 0 {
		for _, v := range p.Proxies {
			ps = v
			break
		}
	}

	if ps == nil {
		return nil, status.Error(codes.NotFound, "proxy not found")
	}

	var targetNodes []pool.Backend
	if req.Address != "" {
		for _, n := range ps.Backends {
			if n.Address() == req.Address {
				targetNodes = append(targetNodes, n)
				break
			}
		}
	} else {
		targetNodes = ps.Backends
	}

	if len(targetNodes) == 0 {
		return nil, status.Error(codes.NotFound, "no backends found")
	}

	var tuner insights.Tuner
	switch p.Config.Protocol {
	case "postgres":
		tuner = insights.NewPostgres()
	default:
		return nil, status.Errorf(codes.Unimplemented, "tuning not supported for protocol: %s", p.Config.Protocol)
	}

	response := &endpoints.TuneDatabaseResponse{
		Nodes: make([]*endpoints.TuneDatabaseResponse_NodeResult, 0, len(targetNodes)),
	}

	for _, node := range targetNodes {
		var metrics *domain.SystemMetrics
		client := node.AgentClient()
		if client != nil {
			resp, err := client.GetSystemInfo(ctx, &endpoints.GetSystemInfoRequest{})
			if err == nil {
				metrics = resp.Metrics
			}
		}

		// Fallback to local metrics if agent is not available or failed
		if metrics == nil {
			rawMetrics := observability2.CollectSystemMetrics()
			metrics = &domain.SystemMetrics{
				CpuUsagePercent:     float32(rawMetrics.CPUUsagePercent),
				CpuCores:            uint32(rawMetrics.CPUCores),
				MemoryTotalBytes:    rawMetrics.MemoryTotalBytes,
				MemoryUsedBytes:     rawMetrics.MemoryUsedBytes,
				MemoryUsagePercent:  float32(rawMetrics.MemoryUsagePercent),
				StorageTotalBytes:   rawMetrics.StorageTotalBytes,
				StorageUsedBytes:    rawMetrics.StorageUsedBytes,
				StorageUsagePercent: float32(rawMetrics.StorageUsagePercent),
				Goroutines:          uint64(rawMetrics.Goroutines),
			}
		}

		result := tuner.Tune(ctx, metrics)
		response.Nodes = append(response.Nodes, &endpoints.TuneDatabaseResponse_NodeResult{
			Address:      node.Address(),
			Suggestions:  result.Suggestions,
			SystemChecks: result.SystemChecks,
		})
	}

	return response, nil
}

func (m *Observability) ApplyTuning(ctx context.Context, req *endpoints.ApplyTuningRequest) (*endpoints.ApplyTuningResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	p.Mu.RLock()
	defer p.Mu.RUnlock()

	var ps *state.Proxy
	if req.ProxyId != "" {
		ps = p.Proxies[req.ProxyId]
	} else if len(p.Proxies) > 0 {
		for _, v := range p.Proxies {
			ps = v
			break
		}
	}

	if ps == nil {
		return nil, status.Error(codes.NotFound, "proxy not found")
	}

	var targetNode pool.Backend
	if req.Address != "" {
		for _, n := range ps.Backends {
			if n.Address() == req.Address {
				targetNode = n
				break
			}
		}
	} else {
		if len(ps.Backends) > 0 {
			targetNode = ps.Backends[0]
		}
	}

	if targetNode == nil {
		return nil, status.Error(codes.NotFound, "backend not found")
	}

	var tuner insights.Tuner
	switch p.Config.Protocol {
	case "postgres":
		tuner = insights.NewPostgres()
	default:
		return nil, status.Errorf(codes.Unimplemented, "tuning not supported for protocol: %s", p.Config.Protocol)
	}

	// Acquire a connection to the backend
	conn, err := targetNode.Acquire(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire backend connection: %v", err)
	}
	defer targetNode.Release(conn)

	// Apply the suggestion
	if err := tuner.Apply(ctx, p.Handler, conn, req.Suggestion); err != nil {
		return &endpoints.ApplyTuningResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	return &endpoints.ApplyTuningResponse{
		Success: true,
		Message: "Tuning recommendation applied successfully.",
	}, nil
}

func (m *Observability) GetPostgresInsights(ctx context.Context, req *endpoints.GetBackendPostgresInsightsRequest) (*endpoints.GetBackendPostgresInsightsResponse, error) {
	p, err := m.registry.GetProjectState(req.ProjectId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	p.Mu.RLock()
	defer p.Mu.RUnlock()

	ps := p.Proxies[req.ProxyId]
	if ps == nil {
		return nil, status.Error(codes.NotFound, "proxy not found")
	}

	var targetNode pool.Backend
	for _, n := range ps.Backends {
		if n.Address() == req.Address {
			targetNode = n
			break
		}
	}

	if targetNode == nil {
		return nil, status.Error(codes.NotFound, "backend not found")
	}

	client := targetNode.AgentClient()
	if client == nil {
		return nil, status.Error(codes.FailedPrecondition, "agent not connected for this backend")
	}

	resp, err := client.GetPostgresInsights(ctx, &endpoints.GetPostgresInsightsRequest{
		Database: "postgres",
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get insights from agent: %v", err)
	}

	return &endpoints.GetBackendPostgresInsightsResponse{
		TopQueries:        resp.TopQueries,
		ActiveLocks:       resp.ActiveLocks,
		ReplicationStatus: resp.ReplicationStatus,
	}, nil
}

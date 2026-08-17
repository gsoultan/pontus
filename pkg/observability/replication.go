package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Replication and routing metrics.
//
// Cardinality is bounded by the number of configured backends, which is an
// operator-set list — never by anything a client sends. A backend address is
// safe as a label for the same reason `pontus_db_*` already uses it; a query, a
// client address or a username would not be.
//
// These exist because the failure they describe is silent. A replica that has
// lost its WAL receiver still answers, still looks healthy, and returns rows
// that get older every second. Nothing in a connection count or an error rate
// moves when that happens, so without these an operator finds out from a user.
var (
	// BackendRole is 1 for the role the backend currently holds, 0 for the
	// other. Two series per backend rather than a string-valued gauge, because
	// Prometheus has no string values and putting the role in a label would
	// leave a stale series behind on every promotion.
	BackendRole = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_backend_role",
		Help: "1 when the backend currently holds this role, 0 otherwise",
	}, []string{"address", "role"})

	// ReplicaLagSeconds is the staleness figure routing actually uses — zero
	// only when a replica is both streaming and caught up. It is deliberately
	// not the raw replay age: an idle primary makes that grow on a perfectly
	// healthy replica.
	ReplicaLagSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_replica_lag_seconds",
		Help: "Replication lag used for routing decisions, in seconds",
	}, []string{"address"})

	// ReplicaStreaming is 0 for the state that matters most and is hardest to
	// see: a replica that is up and answering with no WAL receiver attached.
	ReplicaStreaming = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_replica_streaming",
		Help: "1 when the replica has a WAL receiver attached to a primary, 0 when it is cut off",
	}, []string{"address"})

	// ReplicaReadEligible reflects the routing decision itself, after the lag
	// gate and the reattach dwell. It can be 0 while streaming is 1 — a replica
	// serving out its dwell after a reconnect — and the difference is exactly
	// what an operator asks about when reads pile onto the primary.
	ReplicaReadEligible = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_replica_read_eligible",
		Help: "1 when the replica is currently eligible to serve reads",
	}, []string{"address"})

	// RoutedRequests counts where requests were sent, split by intent. The gap
	// between read-intent requests and reads landing on a replica is the health
	// of the read/write split in one number.
	RoutedRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pontus_routed_requests_total",
		Help: "Requests routed to a backend, by intended access and the role that served it",
	}, []string{"address", "intent", "role"})

	// IdentityMismatches counts pooled connections drawn for one user and found
	// to belong to another.
	//
	// Zero is the expected value once pools are keyed by identity. Anything else
	// is churn: each one is a connection destroyed and an acquisition retried,
	// and a rising rate means users are contending for the same undifferentiated
	// pool.
	IdentityMismatches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pontus_pool_identity_mismatches_total",
		Help: "Pooled connections drawn for one identity and found to belong to another",
	}, []string{"address"})

	// FailoverState mirrors the failover manager's state machine.
	FailoverState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pontus_failover_state",
		Help: "1 when the failover manager is in this state, 0 otherwise",
	}, []string{"state"})

	// FailoverPromotions counts completed promotions. A counter rather than a
	// gauge: what matters afterwards is that it happened and when, not that the
	// state machine has since returned to idle.
	FailoverPromotions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pontus_failover_promotions_total",
		Help: "Replicas promoted to primary by the failover manager",
	}, []string{"address"})

	// FollowPrimaryResults counts replicas re-pointed after a promotion.
	// A failure here leaves a replica streaming from a node that no longer
	// exists, which nothing else reports.
	FollowPrimaryResults = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pontus_follow_primary_total",
		Help: "Attempts to re-point a surviving replica at a newly promoted primary",
	}, []string{"result"})
)

// backendRoles and failoverStates are the full label sets. Every member is
// written on each update, so the one that no longer applies reads 0 instead of
// lingering at its last value.
var (
	backendRoles   = []string{"primary", "replica"}
	failoverStates = []string{"idle", "monitoring", "promoting", "verifying", "failed"}
)

// exclusive returns the value each candidate series should take when active is
// the current one.
//
// Split out from the gauge writes because this is the part with a bug in it: a
// role gauge that only sets the new role leaves the old one reading 1, and an
// operator alerting on two primaries then sees two primaries forever and learns
// to ignore the alert.
func exclusive(candidates []string, active string) map[string]float64 {
	out := make(map[string]float64, len(candidates))
	for _, candidate := range candidates {
		if candidate == active {
			out[candidate] = 1
			continue
		}
		out[candidate] = 0
	}
	return out
}

// SetBackendRole publishes the role as a pair of series.
func SetBackendRole(address, role string) {
	for candidate, value := range exclusive(backendRoles, role) {
		BackendRole.WithLabelValues(address, candidate).Set(value)
	}
}

// SetFailoverState does the same for the failover state machine.
func SetFailoverState(state string) {
	for candidate, value := range exclusive(failoverStates, state) {
		FailoverState.WithLabelValues(candidate).Set(value)
	}
}

// Bool converts a condition to the 0/1 a gauge needs.
func Bool(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

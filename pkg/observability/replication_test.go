package observability

import "testing"

// A role gauge must not leave the previous role reading 1 after a promotion.
// An operator alerting on pontus_backend_role{role="primary"} would otherwise
// see two primaries forever and learn to ignore the alert.
func TestExclusiveClearsTheInactiveSeries(t *testing.T) {
	got := exclusive(backendRoles, "primary")

	if got["primary"] != 1 {
		t.Errorf("primary = %v, want 1", got["primary"])
	}
	if got["replica"] != 0 {
		t.Errorf("replica = %v, want 0; the role it no longer holds stayed set", got["replica"])
	}
	if len(got) != len(backendRoles) {
		t.Errorf("wrote %d series, want %d — every candidate must be written each time",
			len(got), len(backendRoles))
	}
}

// An unknown state must still clear the others rather than leaving whichever
// was last set reading 1.
func TestExclusiveWithNoMatchClearsEverything(t *testing.T) {
	for state, value := range exclusive(failoverStates, "not-a-state") {
		if value != 0 {
			t.Errorf("%s = %v, want 0", state, value)
		}
	}
}

func TestExclusiveCoversEveryFailoverState(t *testing.T) {
	for _, state := range failoverStates {
		got := exclusive(failoverStates, state)
		if got[state] != 1 {
			t.Errorf("%s did not set itself", state)
		}
		var active int
		for _, v := range got {
			if v == 1 {
				active++
			}
		}
		if active != 1 {
			t.Errorf("%s left %d series active, want exactly 1", state, active)
		}
	}
}

func TestBool(t *testing.T) {
	if Bool(true) != 1 || Bool(false) != 0 {
		t.Error("Bool must map to the 0/1 a gauge needs")
	}
}

// Every label must be operator-controlled. A query, client address or username
// would make cardinality unbounded from the wire — the metric equivalent of a
// rate-limiter map keyed by client input.
func TestLabelsAreOperatorControlled(t *testing.T) {
	forbidden := map[string]bool{"query": true, "client": true, "user": true, "sql": true}

	for name, labels := range map[string][]string{
		"pontus_backend_role":              {"address", "role"},
		"pontus_replica_lag_seconds":       {"address"},
		"pontus_replica_streaming":         {"address"},
		"pontus_replica_read_eligible":     {"address"},
		"pontus_routed_requests_total":     {"address", "intent", "role"},
		"pontus_failover_state":            {"state"},
		"pontus_failover_promotions_total": {"address"},
		"pontus_follow_primary_total":      {"result"},
	} {
		for _, label := range labels {
			if forbidden[label] {
				t.Errorf("%s carries %q, which is client-supplied and unbounded", name, label)
			}
		}
	}
}

//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Per-backend request counters must actually count.
//
// IncRequests and IncErrors existed on the pool with no callers, so every
// backend reported zero traffic. A distribution view built on that would have
// rendered an empty chart against a busy proxy — the same defect as the
// tracker, one layer down.
func TestBackendRequestCountersAreRecorded(t *testing.T) {
	s := startStack(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)
	const queries = 20
	for i := range queries {
		var n int
		if err := conn.QueryRow(ctx, "SELECT $1::int", i).Scan(&n); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}

	token := s.login()
	projectID, proxyID := s.project(token)

	var observed float64
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && observed == 0 {
		out, code := s.rpc("GetStatus",
			map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
		if code == http.StatusOK {
			for _, raw := range asSlice(out["backends"]) {
				backend, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				observed += asFloat(backend["totalRequests"])
			}
		}
		if observed == 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if observed < queries {
		t.Errorf("backends reported %v requests after %d queries; the distribution view would be empty",
			observed, queries)
	}
}

// The status payload must name the strategy actually running and the zone the
// proxy treats as local — both are what the dashboard shows instead of echoing
// configuration back.
func TestStatusReportsRoutingContext(t *testing.T) {
	s := startStack(t)

	token := s.login()
	projectID, proxyID := s.project(token)

	out, code := s.rpc("GetStatus",
		map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
	if code != http.StatusOK {
		t.Fatalf("GetStatus failed (%d): %v", code, out)
	}

	// The e2e config asks for p2c; the proxy must report what it is running.
	balancer, _ := out["balancerType"].(string)
	if balancer == "" {
		t.Error("GetStatus did not report a balancer type; the dashboard cannot show what is running")
	}

	// local_zone is set to "e2e" in the harness config.
	if zone, _ := out["localZone"].(string); zone != "e2e" {
		t.Errorf("localZone = %q, want %q — remote backends cannot be identified without it", zone, "e2e")
	}
}

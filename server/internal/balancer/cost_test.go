package balancer

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// Every cost term below the latency blend used to be unreachable.
//
// CalculateCost returned early on a zero latency, and nothing reported latency,
// so every backend scored 0 — an idle node and a saturated one ranked the same,
// and least_conn, p2c and peak_ewma all chose by a constant. For a product that
// calls itself a load balancer this is the load balancing.
func TestCostSeparatesIdleFromSaturatedWithoutTimingData(t *testing.T) {
	idle := &mockBackend{address: "idle", healthy: true, role: pool.RolePrimary}
	busy := &mockBackend{address: "busy", healthy: true, role: pool.RolePrimary, activeConns: 500}

	idleCost := CalculateCost(idle, "")
	busyCost := CalculateCost(busy, "")

	if idleCost <= 0 {
		t.Fatalf("an untried backend costs %v; a zero cost discards every other term", idleCost)
	}
	if busyCost <= idleCost {
		t.Errorf("saturated costs %v and idle costs %v — the balancer cannot tell them apart",
			busyCost, idleCost)
	}
}

// Weight has to matter, or a deliberately smaller node takes an equal share.
func TestCostRespectsWeightWithoutTimingData(t *testing.T) {
	light := &mockBackend{address: "light", healthy: true, role: pool.RolePrimary, weight: 1}
	heavy := &mockBackend{address: "heavy", healthy: true, role: pool.RolePrimary, weight: 10}

	if CalculateCost(heavy, "") >= CalculateCost(light, "") {
		t.Error("a backend with ten times the weight does not cost less")
	}
}

// Service time is the signal the cost function is named for.
func TestCostRisesWithLatency(t *testing.T) {
	fast := &mockBackend{address: "fast", healthy: true, role: pool.RolePrimary,
		latency: time.Millisecond}
	slow := &mockBackend{address: "slow", healthy: true, role: pool.RolePrimary,
		latency: 200 * time.Millisecond}

	if CalculateCost(slow, "") <= CalculateCost(fast, "") {
		t.Error("a slower backend does not cost more")
	}
}

// A backend that has only reported time-to-first-byte still has to rank.
func TestCostUsesRTTWhenLatencyIsMissing(t *testing.T) {
	quiet := &mockBackend{address: "quiet", healthy: true, role: pool.RolePrimary}
	distant := &mockBackend{address: "distant", healthy: true, role: pool.RolePrimary,
		rtt: 50 * time.Millisecond}

	if CalculateCost(distant, "") <= CalculateCost(quiet, "") {
		t.Error("RTT is ignored when latency is absent, so a distant backend looks free")
	}
}

// Locality only applies when the caller says where it is.
func TestCostAppliesTheRemoteZonePenalty(t *testing.T) {
	local := &mockBackend{address: "local", healthy: true, role: pool.RolePrimary,
		latency: time.Millisecond, zone: "eu"}
	remote := &mockBackend{address: "remote", healthy: true, role: pool.RolePrimary,
		latency: time.Millisecond, zone: "us"}

	if CalculateCost(remote, "eu") <= CalculateCost(local, "eu") {
		t.Error("a backend in another zone is not penalised")
	}
	if CalculateCost(remote, "") != CalculateCost(local, "") {
		t.Error("a zone penalty was applied when the caller did not say where it is")
	}
}

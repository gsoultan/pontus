package registry

import (
	"fmt"
	"testing"

	balancer2 "github.com/gsoultan/pontus/server/internal/balancer"
	pool2 "github.com/gsoultan/pontus/server/internal/pool"
)

// Four of the six strategies were unreachable: the selection switch handled
// "consistent" and sent everything else to round-robin, so a config asking for
// p2c or least-conns silently ran round-robin.
func TestNewBalancerResolvesEveryStrategy(t *testing.T) {
	var backends []pool2.Backend

	cases := []struct {
		name string
		want any
	}{
		{"round-robin", &balancer2.RoundRobin{}},
		{"", &balancer2.RoundRobin{}},
		{"weighted-round-robin", &balancer2.WeightedRoundRobin{}},
		{"weighted", &balancer2.WeightedRoundRobin{}},
		{"least-conns", &balancer2.LeastConn{}},
		{"least-connections", &balancer2.LeastConn{}},
		{"p2c", &balancer2.P2C{}},
		{"peak-ewma", &balancer2.PeakEWMA{}},
		{"ewma", &balancer2.PeakEWMA{}},
		{"consistent", &balancer2.ConsistentHash{}},
		{"ip-hash", &balancer2.ConsistentHash{}},
		// Case and surrounding whitespace must not change the result.
		{"  P2C  ", &balancer2.P2C{}},
		{"Least-Conns", &balancer2.LeastConn{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newBalancer(tc.name, backends)
			if gotType, wantType := typeName(got), typeName(tc.want); gotType != wantType {
				t.Errorf("newBalancer(%q) = %s, want %s", tc.name, gotType, wantType)
			}
		})
	}
}

// An unrecognised name must still yield a working balancer rather than nil.
func TestNewBalancerFallsBackForUnknown(t *testing.T) {
	var backends []pool2.Backend

	got := newBalancer("does-not-exist", backends)
	if got == nil {
		t.Fatal("newBalancer returned nil for an unknown strategy")
	}
	if typeName(got) != typeName(&balancer2.RoundRobin{}) {
		t.Errorf("unknown strategy = %s, want RoundRobin", typeName(got))
	}
}

func typeName(v any) string {
	return fmt.Sprintf("%T", v)
}

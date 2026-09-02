package balancer

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// selectable is a backend with exactly the properties FilterNodes reads.
type selectable struct {
	pool.Backend
	addr        string
	role        pool.Role
	healthy     bool
	draining    bool
	replicating bool
	lag         time.Duration
}

func (s *selectable) Address() string               { return s.addr }
func (s *selectable) Role() pool.Role               { return s.role }
func (s *selectable) IsHealthy() bool               { return s.healthy }
func (s *selectable) IsDraining() bool              { return s.draining }
func (s *selectable) IsReplicating() bool           { return s.replicating }
func (s *selectable) ReplicationLag() time.Duration { return s.lag }
func (s *selectable) Zone() string                  { return "" }

func primaryNode(addr string, healthy bool) *selectable {
	return &selectable{addr: addr, role: pool.RolePrimary, healthy: healthy}
}

func replicaNode(addr string, healthy, streaming bool) *selectable {
	return &selectable{addr: addr, role: pool.RoleReplica, healthy: healthy,
		replicating: streaming}
}

func addresses(p *[]pool.Backend) []string {
	out := make([]string, 0, len(*p))
	for _, b := range *p {
		out = append(out, b.Address())
	}
	return out
}

// A session's startup connection is not a write. Insisting on a primary for it
// meant that while the primary was down no session could be opened at all — not
// even a read-only one — so a deployment that keeps replicas for exactly that
// outage could not open a session to use them.
func TestAcceptReplicaOnlyAppliesWhenThereIsNoPrimary(t *testing.T) {
	healthyPrimary := primaryNode("primary:5432", true)
	deadPrimary := primaryNode("primary:5432", false)
	replica := replicaNode("replica:5432", true, true)

	for name, tc := range map[string]struct {
		nodes []pool.Backend
		hint  Hint
		want  []string
	}{
		"a write takes the primary": {
			nodes: []pool.Backend{healthyPrimary, replica},
			hint:  Hint{ReadOnly: false},
			want:  []string{"primary:5432"},
		},
		// The property that must never break: a write settling for a replica is
		// a write that fails, or one that silently does not happen.
		"a write with no primary takes nothing": {
			nodes: []pool.Backend{deadPrimary, replica},
			hint:  Hint{ReadOnly: false},
			want:  nil,
		},
		"a startup connection still prefers the primary": {
			nodes: []pool.Backend{healthyPrimary, replica},
			hint:  Hint{ReadOnly: false, AcceptReplica: true},
			want:  []string{"primary:5432"},
		},
		"a startup connection takes a replica when there is no primary": {
			nodes: []pool.Backend{deadPrimary, replica},
			hint:  Hint{ReadOnly: false, AcceptReplica: true},
			want:  []string{"replica:5432"},
		},
		"a startup connection takes nothing when nothing is healthy": {
			nodes: []pool.Backend{deadPrimary, replicaNode("replica:5432", false, false)},
			hint:  Hint{ReadOnly: false, AcceptReplica: true},
			want:  nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := FilterNodes(tc.nodes, tc.hint)
			defer PutTargets(got)

			addrs := addresses(got)
			if len(addrs) != len(tc.want) {
				t.Fatalf("selected %v, want %v", addrs, tc.want)
			}
			for i := range addrs {
				if addrs[i] != tc.want[i] {
					t.Errorf("selected %v, want %v", addrs, tc.want)
				}
			}
		})
	}
}

// The fallback picks the same way a read does: a streaming replica inside the
// lag budget before a stale one, so a session that lands here lands on the
// freshest node rather than merely the first healthy one.
func TestStartupFallbackPrefersAStreamingReplica(t *testing.T) {
	stale := replicaNode("stale:5432", true, false)
	streaming := replicaNode("streaming:5432", true, true)
	nodes := []pool.Backend{primaryNode("primary:5432", false), stale, streaming}

	got := FilterNodes(nodes, Hint{ReadOnly: false, AcceptReplica: true})
	defer PutTargets(got)

	addrs := addresses(got)
	if len(addrs) != 1 || addrs[0] != "streaming:5432" {
		t.Errorf("selected %v, want only the streaming replica", addrs)
	}
}

// And falls back to a stale one rather than nothing: with no primary, a stale
// answer beats having nowhere to go at all.
func TestStartupFallbackTakesAStaleReplicaOverNothing(t *testing.T) {
	nodes := []pool.Backend{
		primaryNode("primary:5432", false),
		replicaNode("stale:5432", true, false),
	}

	got := FilterNodes(nodes, Hint{ReadOnly: false, AcceptReplica: true})
	defer PutTargets(got)

	if addrs := addresses(got); len(addrs) != 1 || addrs[0] != "stale:5432" {
		t.Errorf("selected %v, want the stale replica", addrs)
	}
}

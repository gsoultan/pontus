package balancer

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// Routing runs on every statement: FilterNodes builds the candidate set and
// CalculateCost ranks it. AGENTS.md requires a benchmark before anything on
// this path grows an allocation, and there was none to hold it to.
//
//	go test ./server/internal/balancer/ -run '^$' -bench . -benchmem -count=10

// benchBackend is a backend whose measurements are fixed, so a benchmark
// measures the routing code rather than the doubles behind it.
type benchBackend struct {
	pool.Backend
	addr        string
	role        pool.Role
	healthy     bool
	lag         time.Duration
	replicating bool
}

func (b *benchBackend) Address() string               { return b.addr }
func (b *benchBackend) Role() pool.Role               { return b.role }
func (b *benchBackend) IsHealthy() bool               { return b.healthy }
func (b *benchBackend) IsDraining() bool              { return false }
func (b *benchBackend) IsReplicating() bool           { return b.replicating }
func (b *benchBackend) ReplicationLag() time.Duration { return b.lag }
func (b *benchBackend) Zone() string                  { return "local" }
func (b *benchBackend) Weight() int                   { return 1 }
func (b *benchBackend) ActiveConns() int64            { return 4 }
func (b *benchBackend) ErrorRate() float64            { return 0 }
func (b *benchBackend) Latency() time.Duration        { return 2 * time.Millisecond }
func (b *benchBackend) RTT() time.Duration            { return 500 * time.Microsecond }
func (b *benchBackend) LastHealthy() time.Time        { return time.Now().Add(-time.Hour) }

func benchNodes(replicas int) []pool.Backend {
	nodes := []pool.Backend{
		&benchBackend{addr: "primary:5432", role: pool.RolePrimary, healthy: true, replicating: true},
	}
	for i := range replicas {
		nodes = append(nodes, &benchBackend{
			addr:        "replica:" + string(rune('a'+i)),
			role:        pool.RoleReplica,
			healthy:     true,
			replicating: true,
			lag:         time.Duration(i) * time.Second,
		})
	}
	return nodes
}

var costSink float64

func BenchmarkCalculateCost(b *testing.B) {
	node := benchNodes(1)[1]

	b.ReportAllocs()
	for b.Loop() {
		costSink = CalculateCost(node, "local")
	}
}

// FilterNodes reuses a sync.Pool of slices to stay allocation-free. That is a
// property worth a number rather than a comment.
func BenchmarkFilterNodes(b *testing.B) {
	for _, size := range []int{1, 3, 8} {
		nodes := benchNodes(size)

		b.Run("read/"+string(rune('0'+size)), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				targets := FilterNodes(nodes, Hint{ReadOnly: true, CallerZone: "local"})
				PutTargets(targets)
			}
		})

		b.Run("write/"+string(rune('0'+size)), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				targets := FilterNodes(nodes, Hint{CallerZone: "local"})
				PutTargets(targets)
			}
		})
	}
}

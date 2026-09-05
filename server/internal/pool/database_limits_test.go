package pool

import (
	"testing"
	"time"

	"github.com/gsoultan/gpool/pkg/pooling"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// A per-database rule is a cap, not a target. The adaptive controller lowers
// capacity for the whole backend under pressure, and a per-database value that
// overrode it would let one tenant keep the connections the controller is
// trying to reclaim.
func TestCeilingForTakesTheLower(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured int32
		limit      int32
		want       int32
	}{
		{"no rule for this database", 20, 0, 20},
		{"rule below the backend ceiling", 20, 5, 5},
		{"rule above the backend ceiling does not raise it", 20, 50, 20},
		{"rule equal to the ceiling", 20, 20, 20},
		{"controller lowered below the rule", 3, 5, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ceilingFor(tc.configured, tc.limit); got != tc.want {
				t.Errorf("ceilingFor(%d, %d) = %d, want %d",
					tc.configured, tc.limit, got, tc.want)
			}
		})
	}
}

func TestServerDatabaseLimitsAreSwappable(t *testing.T) {
	p := &Server{}

	// No limits installed means no opinion, so every identity takes the global
	// ceiling.
	if got := p.limitFor("app"); got != 0 {
		t.Errorf("limitFor with no limits = %d, want 0", got)
	}

	p.SetDatabaseLimits(func(database string) int32 {
		if database == "app" {
			return 5
		}
		return 0
	})
	if got := p.limitFor("app"); got != 5 {
		t.Errorf("limitFor(app) = %d, want 5", got)
	}
	if got := p.limitFor("reporting"); got != 0 {
		t.Errorf("limitFor(reporting) = %d, want 0", got)
	}

	// A reload that removes the table must remove the limits with it, not
	// leave the previous ones applying to a configuration that no longer
	// mentions them.
	p.SetDatabaseLimits(nil)
	if got := p.limitFor("app"); got != 0 {
		t.Errorf("limitFor after clearing = %d, want 0", got)
	}
}

// The ceiling has to be baked into the pool when it is built, because pools are
// created lazily on first acquire — a limit consulted only at startup would
// never reach the pool it was meant for.
func TestPoolSetBuildsEachIdentityWithItsOwnCeiling(t *testing.T) {
	limits := map[string]int32{"small": 1, "large": 4}

	set := newPoolSet("test:5432", 100, 8, time.Minute,
		func(id identity) (*pooling.Core[*Conn], error) {
			max := int32(10)
			if limit, ok := limits[id.database]; ok {
				max = limit
			}
			return pooling.New[*Conn](&connDriver{address: "test:5432"}, pooling.Config{
				MaxConns: max,
			}.WithDefaults())
		})
	defer set.close()

	for _, tc := range []struct {
		database string
		want     int32
	}{
		{"small", 1},
		{"large", 4},
		{"unlisted", 10},
	} {
		core, err := set.get(identity{user: "app", database: tc.database})
		if err != nil {
			t.Fatalf("get(%s): %v", tc.database, err)
		}
		if got := core.Stat().MaxConnections(); got != tc.want {
			t.Errorf("%s pool ceiling = %d, want %d", tc.database, got, tc.want)
		}
	}
}

// The adaptive controller resizes the whole backend. A per-database rule has to
// survive that: the controller's job is to lower capacity under pressure, not
// to overrule the ceiling an operator set for one tenant.
func TestSetMaxConnsRespectsPerDatabaseCeilings(t *testing.T) {
	addr := listenAndHold(t)

	const backendMax = 8
	p, err := NewServer(addr, "", "127.0.0.1:9092", "test-token", RolePrimary, 1,
		backendMax, 0, time.Second, protocol.NewPostgresHandler(), nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	p.SetDatabaseLimits(func(database string) int32 {
		if database == "bounded" {
			return 2
		}
		return 0
	})

	bounded, err := p.pools.get(identity{user: "app", database: "bounded"})
	if err != nil {
		t.Fatalf("creating the bounded pool: %v", err)
	}
	free, err := p.pools.get(identity{user: "app", database: "free"})
	if err != nil {
		t.Fatalf("creating the unbounded pool: %v", err)
	}

	if got := bounded.Stat().MaxConnections(); got != 2 {
		t.Errorf("bounded pool started at %d, want its rule of 2", got)
	}
	if got := free.Stat().MaxConnections(); got != backendMax {
		t.Errorf("unbounded pool started at %d, want the global %d", got, backendMax)
	}

	// The controller raises the backend to its ceiling. The bounded database
	// must stay where its rule put it.
	if err := p.SetMaxConns(backendMax); err != nil {
		t.Fatalf("SetMaxConns: %v", err)
	}
	if got := bounded.Stat().MaxConnections(); got != 2 {
		t.Errorf("a resize raised the bounded pool to %d, past its rule of 2", got)
	}
	if got := free.Stat().MaxConnections(); got != backendMax {
		t.Errorf("unbounded pool = %d, want %d", got, backendMax)
	}

	// The controller lowers the backend below the rule. Now the controller's
	// value is the lower of the two and must win.
	if err := p.SetMaxConns(1); err != nil {
		t.Fatalf("SetMaxConns: %v", err)
	}
	if got := bounded.Stat().MaxConnections(); got != 1 {
		t.Errorf("bounded pool = %d, want the controller's 1", got)
	}
	if got := free.Stat().MaxConnections(); got != 1 {
		t.Errorf("unbounded pool = %d, want the controller's 1", got)
	}
}

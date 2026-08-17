package pool

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/gpool/pkg/pooling"
)

// stubCore builds a pool that never dials, so the set's own behaviour can be
// tested without a database.
func stubSet(t *testing.T, maxTotal int32, maxPools int, ttl time.Duration) *poolSet {
	t.Helper()

	created := 0
	return newPoolSet("test:5432", maxTotal, maxPools, ttl,
		func() (*pooling.Core[*Conn], error) {
			created++
			return pooling.New[*Conn](&connDriver{address: "test:5432"}, pooling.Config{
				MaxConns: 1,
			}.WithDefaults())
		})
}

// One pool per identity is the whole point: a connection carries the
// credentials it authenticated with.
func TestPoolSetKeepsIdentitiesApart(t *testing.T) {
	set := stubSet(t, 0, 16, 0)
	t.Cleanup(set.close)

	alice, err := set.get(identity{user: "alice", database: "sales"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := set.get(identity{user: "bob", database: "sales"})
	if err != nil {
		t.Fatal(err)
	}
	if alice == bob {
		t.Error("two users were given the same pool; one would be served on the other's connection")
	}

	again, err := set.get(identity{user: "alice", database: "sales"})
	if err != nil {
		t.Fatal(err)
	}
	if again != alice {
		t.Error("the same identity was given a second pool")
	}

	// The same user on a different database is a different identity: a
	// connection is bound to the database it opened.
	other, err := set.get(identity{user: "alice", database: "hr"})
	if err != nil {
		t.Fatal(err)
	}
	if other == alice {
		t.Error("two databases shared a pool")
	}
}

// The map is keyed by a user name from a startup packet, which anyone who can
// reach the port chooses. Unbounded, that is a remote memory exhaustion and an
// unbounded number of real connections on the database.
func TestPoolSetBoundsTheNumberOfPools(t *testing.T) {
	const limit = 8
	set := stubSet(t, 0, limit, 0)
	t.Cleanup(set.close)

	for i := range 200 {
		if _, err := set.get(identity{user: "user-" + strconv.Itoa(i), database: "db"}); err != nil {
			// Refusing is acceptable; growing without bound is not.
			if !errors.Is(err, ErrBackendAtCapacity) {
				t.Fatalf("unexpected error: %v", err)
			}
		}
	}

	if got := set.count(); got > limit {
		t.Errorf("holding %d pools with a limit of %d", got, limit)
	}
}

// A pool nobody has used should not hold connections for the life of the
// process.
func TestPoolSetReapsIdlePools(t *testing.T) {
	set := stubSet(t, 0, 16, 50*time.Millisecond)
	t.Cleanup(set.close)

	if _, err := set.get(identity{user: "transient", database: "db"}); err != nil {
		t.Fatal(err)
	}
	if set.count() != 1 {
		t.Fatalf("expected one pool, got %d", set.count())
	}

	time.Sleep(120 * time.Millisecond)

	// Reaping happens on use, so asking for another identity triggers it.
	if _, err := set.get(identity{user: "arriving", database: "db"}); err != nil {
		t.Fatal(err)
	}
	if set.count() != 1 {
		t.Errorf("holding %d pools; the idle one was not reaped", set.count())
	}
}

// A closed set must not hand out more pools.
func TestPoolSetRefusesAfterClose(t *testing.T) {
	set := stubSet(t, 0, 16, 0)
	set.close()

	if _, err := set.get(identity{user: "late", database: "db"}); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("err = %v, want ErrPoolClosed", err)
	}
}

// Pontus's own work uses a distinct identity, so a health probe cannot borrow a
// session's connection or count against a tenant's ceiling.
func TestSystemIdentityIsItsOwnPool(t *testing.T) {
	set := stubSet(t, 0, 16, 0)
	t.Cleanup(set.close)

	system, err := set.get(identity{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := set.get(identity{user: "alice", database: "sales"})
	if err != nil {
		t.Fatal(err)
	}
	if system == client {
		t.Error("health probes share a pool with a client")
	}
	if got := (identity{}).String(); got != "system" {
		t.Errorf("the empty identity prints as %q", got)
	}
}

// The backend ceiling has to exist, because per-pool alone has no upper bound.
func TestBackendConnCeilingIsFinite(t *testing.T) {
	if got := backendConnCeiling(20); got <= 20 {
		t.Errorf("ceiling %d is not above the per-identity limit", got)
	}
	if got := backendConnCeiling(0); got != 0 {
		t.Errorf("an unset per-identity limit should leave the ceiling unset, got %d", got)
	}
}

// Totals are summed across pools: the dashboard and the adaptive controller
// care about the backend, not one identity's slice of it.
func TestPoolSetTotalsAggregate(t *testing.T) {
	set := stubSet(t, 0, 16, 0)
	t.Cleanup(set.close)

	for _, user := range []string{"a", "b", "c"} {
		if _, err := set.get(identity{user: user, database: "db"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := set.totals().Pools; got != 3 {
		t.Errorf("totals report %d pools, want 3", got)
	}
}

package orchestration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/pontus/server/internal/pool"
)

// immediateFailover promotes on the first observation, which is what these
// tests were written against. Production defaults require repeated failures.
func immediateFailover() Options {
	return Options{Enabled: true, FailureThreshold: 1}
}

type mockBackend struct {
	pool.Backend
	address string

	// Guarded, because the real *pool.Server is: verifyPromotion reads the role
	// from its own goroutine while a test writes it, and a double that is not
	// safe reports a race the production type does not have.
	mu         sync.Mutex
	role       pool.Role
	healthy    bool
	reevaluted int
}

func (m *mockBackend) Address() string { return m.address }

func (m *mockBackend) Role() pool.Role {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.role
}

func (m *mockBackend) setRole(r pool.Role) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.role = r
}

func (m *mockBackend) IsHealthy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.healthy
}

func (m *mockBackend) SetHealthy(h bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthy = h
}

func (m *mockBackend) ReevaluateRole() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reevaluted++
}

func (m *mockBackend) reevaluations() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reevaluted
}

type mockProvisioner struct {
	Provisioner
	promoted string
	demoted  []string
	// repointed maps a replica address to the primary it was told to follow.
	repointed     map[string]string
	failDemoteFor string
	lag           time.Duration
	mu            sync.Mutex
}

func (m *mockProvisioner) PromoteToPrimary(ctx context.Context, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promoted = addr
	return nil
}

func (m *mockProvisioner) DemoteToReplica(ctx context.Context, addr, primary string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDemoteFor == addr {
		return errors.New("agent unreachable")
	}
	m.demoted = append(m.demoted, addr)
	if m.repointed == nil {
		m.repointed = map[string]string{}
	}
	m.repointed[addr] = primary
	return nil
}

func (m *mockProvisioner) CheckReplicationLag(ctx context.Context, addr string) (time.Duration, error) {
	return m.lag, nil
}

type mockConsensus struct {
	Consensus
	leader bool
}

func (m *mockConsensus) IsLeader() bool                  { return m.leader }
func (m *mockConsensus) GetPrimary() (string, error)     { return "p1", nil }
func (m *mockConsensus) SetPrimary(address string) error { return nil }

func TestFailoverManager_AutomaticFailover(t *testing.T) {
	p1 := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	r1 := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{p1, r1}
	provisioner := &mockProvisioner{}
	consensus := &mockConsensus{leader: true}

	mgr := NewFailoverManager(provisioner, consensus, func() []pool.Backend { return backends }, immediateFailover())

	// Manually call monitor instead of Start to avoid non-determinism in test
	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	if provisioner.promoted != "r1" {
		t.Errorf("Expected r1 to be promoted, got %s", provisioner.promoted)
	}
	provisioner.mu.Unlock()
}

func TestFailoverManager_AutomaticFailback_SplitBrain(t *testing.T) {
	p1 := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: true}
	p2 := &mockBackend{address: "p2", role: pool.RolePrimary, healthy: true}

	backends := []pool.Backend{p1, p2}
	provisioner := &mockProvisioner{}
	consensus := &mockConsensus{leader: true}

	mgr := NewFailoverManager(provisioner, consensus, func() []pool.Backend { return backends }, immediateFailover())

	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	if len(provisioner.demoted) != 1 {
		t.Errorf("Expected 1 backend to be demoted, got %d", len(provisioner.demoted))
	} else if provisioner.demoted[0] != "p2" {
		t.Errorf("Expected p2 to be demoted, got %s", provisioner.demoted[0])
	}
	provisioner.mu.Unlock()
}

// Promoting a replica is only half a failover: the proxy still has the node
// recorded as a replica, so it keeps routing writes at the primary that just
// died. The pool re-checks roles on a 30s tick, which means up to half a
// minute of failed writes after a failover the logs already called successful.
//
// The split-brain path already re-evaluates the node it demotes. The failover
// path has to do the same for the node it promotes.
func TestFailoverTellsTheProxyTheWriteNodeMoved(t *testing.T) {
	dead := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	promoted := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{dead, promoted}
	provisioner := &mockProvisioner{}

	mgr := NewFailoverManager(provisioner, &mockConsensus{leader: true},
		func() []pool.Backend { return backends }, immediateFailover())

	if err := mgr.TriggerFailover(t.Context()); err != nil {
		t.Fatalf("TriggerFailover: %v", err)
	}

	if got := promoted.reevaluations(); got == 0 {
		t.Error("the promoted node was never re-evaluated, so the proxy would " +
			"keep sending writes to the old primary until the next 30s role check")
	}
}

// The old primary coming back after a failover is the normal end of a failover,
// not an exotic case: the node reboots, Postgres starts, and it still believes
// it is the primary. Two primaries is the expected transient state.
//
// registry.go constructs the FailoverManager with a nil Consensus — there is no
// Raft in a single-node deployment — and the split-brain branch calls
// m.consensus.GetPrimary() without the nil guard the leader check above it has.
// So the recovery path panics on the monitor goroutine and takes the proxy down.
func TestSplitBrainWithoutConsensusDoesNotPanic(t *testing.T) {
	recovered := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: true}
	promoted := &mockBackend{address: "r1", role: pool.RolePrimary, healthy: true}

	backends := []pool.Backend{recovered, promoted}
	provisioner := &mockProvisioner{}

	// nil consensus, exactly as registry.go builds it.
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends }, immediateFailover())

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("monitor panicked resolving split-brain with no consensus: %v", r)
		}
	}()
	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	demoted := len(provisioner.demoted)
	provisioner.mu.Unlock()
	if demoted != 1 {
		t.Errorf("expected exactly one node demoted, got %d", demoted)
	}
}

// A failover followed by the old primary rebooting must not hand the write
// role back to it.
//
// The promoted replica has already taken writes the old primary never saw. With
// no consensus configured, resolving split-brain by config order picks the
// original primary — undoing the failover and pointing writes at the node that
// just failed, on top of a diverged timeline.
func TestRecoveredPrimaryDoesNotStealBackTheWriteRole(t *testing.T) {
	old := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	replica := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{old, replica}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends }, immediateFailover())

	// The primary dies and r1 is promoted.
	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	promotedAddr := provisioner.promoted
	provisioner.mu.Unlock()
	if promotedAddr != "r1" {
		t.Fatalf("expected r1 promoted, got %q", promotedAddr)
	}
	replica.setRole(pool.RolePrimary)

	// p1 reboots and comes back still believing it is the primary.
	old.SetHealthy(true)
	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	demoted := append([]string(nil), provisioner.demoted...)
	provisioner.mu.Unlock()

	if len(demoted) != 1 {
		t.Fatalf("expected one node demoted, got %v", demoted)
	}
	if demoted[0] != "p1" {
		t.Errorf("demoted %q; the recovered old primary should lose to the node "+
			"that was promoted, not the other way round", demoted[0])
	}
}

// Promotion is irreversible — PostgreSQL starts a new timeline and the old
// primary can no longer simply rejoin — so a single failed check must not be
// enough to trigger one.
func TestFailoverWaitsForRepeatedFailures(t *testing.T) {
	dead := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	replica := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{dead, replica}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, FailureThreshold: 3})

	for i := 1; i < 3; i++ {
		mgr.monitor(t.Context())
		provisioner.mu.Lock()
		promoted := provisioner.promoted
		provisioner.mu.Unlock()
		if promoted != "" {
			t.Fatalf("promoted after %d consecutive failures, threshold is 3", i)
		}
	}

	mgr.monitor(t.Context())
	provisioner.mu.Lock()
	defer provisioner.mu.Unlock()
	if provisioner.promoted != "r1" {
		t.Errorf("no promotion after reaching the threshold; promoted = %q", provisioner.promoted)
	}
}

// A blip must not accumulate toward the threshold across unrelated outages.
func TestFailoverThresholdResetsWhenThePrimaryAnswers(t *testing.T) {
	primary := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	replica := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{primary, replica}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: true, FailureThreshold: 3})

	mgr.monitor(t.Context())
	mgr.monitor(t.Context())

	// The primary was only briefly unreachable.
	primary.SetHealthy(true)
	mgr.monitor(t.Context())

	// It drops again — this must start counting from one, not from three.
	primary.SetHealthy(false)
	mgr.monitor(t.Context())

	provisioner.mu.Lock()
	defer provisioner.mu.Unlock()
	if provisioner.promoted != "" {
		t.Errorf("promoted %q on the first failure after a recovery; "+
			"the counter carried over instead of resetting", provisioner.promoted)
	}
}

// Automatic promotion is off by default, and must stay off when disabled.
func TestFailoverDoesNotPromoteWhenDisabled(t *testing.T) {
	dead := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	replica := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{dead, replica}
	provisioner := &mockProvisioner{}
	mgr := NewFailoverManager(provisioner, nil, func() []pool.Backend { return backends },
		Options{Enabled: false, FailureThreshold: 1})

	for range 5 {
		mgr.monitor(t.Context())
	}

	provisioner.mu.Lock()
	defer provisioner.mu.Unlock()
	if provisioner.promoted != "" {
		t.Errorf("promoted %q with automatic failover disabled", provisioner.promoted)
	}
}

// StateVerifying used to be a thirty-second sleep that then declared success,
// so a promotion the database refused looked exactly like one that worked.
func TestPromotionIsVerifiedAgainstTheNodesActualRole(t *testing.T) {
	dead := &mockBackend{address: "p1", role: pool.RolePrimary, healthy: false}
	promoted := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	backends := []pool.Backend{dead, promoted}
	mgr := NewFailoverManager(&mockProvisioner{}, nil,
		func() []pool.Backend { return backends }, immediateFailover())

	// The node reports itself primary, as a real one would once promotion lands.
	promoted.setRole(pool.RolePrimary)

	mgr.verifyPromotion(t.Context(), promoted)

	if got := mgr.State(); got != StateIdle {
		t.Errorf("state = %v after a confirmed promotion, want Idle", got)
	}
	if promoted.reevaluations() == 0 {
		t.Error("the node was never re-asked; verification would be trusting the " +
			"role recorded at promotion time")
	}
}

// A node that never becomes primary must raise the alarm rather than quietly
// returning to idle: the cluster has no writable node.
func TestUnconfirmedPromotionIsReportedAsFailed(t *testing.T) {
	stuck := &mockBackend{address: "r1", role: pool.RoleReplica, healthy: true}

	mgr := NewFailoverManager(&mockProvisioner{}, nil,
		func() []pool.Backend { return []pool.Backend{stuck} }, immediateFailover())

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	// The context expiring stands in for the budget running out without the
	// test waiting a minute for it.
	mgr.verifyPromotion(ctx, stuck)

	if got := mgr.State(); got == StateVerifying {
		t.Error("left in Verifying; the state has to resolve one way or the other")
	}
}

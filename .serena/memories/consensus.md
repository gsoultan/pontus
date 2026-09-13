# Consensus (`server/internal/consensus`)

**Wired and on-by-configuration since 2026-09-13.** `consensus:` in the config
turns it on; off by default, because one Pontus needs agreement with nobody.

With it on, only the Raft leader acts on the cluster — `FailoverManager.monitor`
returns early on a follower, so promotion, follow-primary and rejoin all stop.
That is the point, and it is why enabling it is a deliberate choice.

**The cross-host half of a pair.** The orchestration lock (`mem:zero_downtime_upgrade`)
stops two Pontus processes *on one host* from both acting, which an overlapping
binary upgrade creates. Consensus stops two *hosts*. Both apply; neither replaces
the other.

## There were two implementations

`orchestration/raft.go` held a second, independent Raft — reachable by neither
caller, satisfying the `Consensus` interface, and carrying **every** defect the
first one had: in-memory stores, a discarded `BootstrapCluster` error, an apply
checked on `Error()` alone, a `Stop` that left the transport open. Deleted
2026-09-13. Two implementations of consensus is how they drift.

## The state of it

`NewNode` has no caller. `registry.go` constructs the failover manager with a
`nil` consensus, which is why `mem:failover` records the nil-guard bug that
wedged `monitor()`. Before 2026-09-11 `*Node` could not have been passed anyway:
it offered `GetConfig`/`LeaderAddr` where `orchestration.Consensus` wants
`GetPrimary`/`LeaderID`/`Start`/`Stop`. Two halves of a feature written against
different shapes.

`TestNodeSatisfiesTheConsensusContract` now pins the contract with a structural
assertion (declared locally — importing `orchestration` would be an import
cycle). If the interface grows a method, that test fails rather than the two
halves drifting apart again.

**Enabling it is a config decision, not a code one**: node id, peers, bootstrap.
Turning cluster-wide agreement on for an existing deployment changes who may
promote, so it was left off deliberately.

## The defect that mattered most (2026-09-13)

**The log and stable store were `raft.NewInmemStore`**, with a comment calling
that lightweight. It is not lightweight, it is unsound. The log holds entries the
node has acknowledged; the stable store holds `currentTerm` and `votedFor`, the
two values that stop a node voting twice in one term. A node that forgets them
and returns can elect a second leader in a term that already has one — the split
brain consensus exists to prevent.

Measured: a node committed a primary and a config, restarted, and came back with
**both empty**, having bootstrapped itself into a fresh cluster. Now
`raft-boltdb/v2` (bbolt, pure Go — CGO_ENABLED=0 unaffected). `Stop` closes the
store **last**, because bolt holds a file lock a restart needs.

### Reads are empty, not stale, just after startup

Intrinsic rather than a bug, and it bit the restart test about one run in three.
A restarted node replays its log into the FSM **asynchronously** and leadership
can arrive first, so a read taken straight after becoming leader returns nothing.
To the failover manager an empty primary is not "ask again", it is "there is no
primary" — a reason to promote one.

`WaitForApplied(ctx)` is the wait a caller that *acts* on a read must do: a
`Barrier` on the leader, and on a follower the applied index reaching the
committed one. A caller that merely displays a value can skip it.

## Config rules that are refused at startup

Every one of these shows up later as "no leader", and a cluster that never elects
one looks exactly like a cluster still waiting to — so the difference is checked
where it is visible, in config.

- **Exactly one node bootstraps**, and only the first time. Several form several
  clusters of one, each with its own leader. A node with existing state ignores
  the flag (`ErrCantBootstrap`), so leaving it in a unit file is safe.
- **`node_id` unique and stable.** Raft records votes against it; duplicates are
  refused because two nodes sharing one can elect two leaders in a term.
- **The data directory must be durable** — see the store defect below.
- Peers are added by the bootstrapping node *after* it wins an election, so that
  runs in a goroutine. Adding an existing voter is a no-op.

## Testing it: use more than one node

The first tests were single-node, which exercises the FSM and the API and **none
of the consensus** — one node elects itself unopposed and commits locally.
`cluster_test.go` runs three on loopback, in-process: same transport, election
and replication code as three hosts, without containers or minutes. It covers
election, replication to followers, a follower refusing writes, surviving the
loss of the leader with committed state intact, and a minority refusing to
commit.

## Four defects the first tests found

Each has a test that fails against the previous code.

1. **A rejected command read as agreement.** `future.Error()` says the entry was
   *committed*; `future.Response()` says whether applying it worked, and an FSM
   returns its errors there. Only the first was checked. **Never report a raft
   apply as successful on `Error()` alone.**
2. **`Restore` merged instead of replacing.** It decoded into live state, and
   `clusterState` omits zero fields, so anything a snapshot did not mention
   survived. Decode into a fresh value and assign.
3. **`GetConfig` returned the FSM's own slice** — replicated state a caller could
   edit without going through the log. Returns `bytes.Clone` now.
4. **`BootstrapCluster`'s error was discarded**, yielding a node that can never
   elect a leader and never says why.

Also fixed: `Apply` rejects an unknown command rather than ignoring it (silently
skipping an entry a peer wrote means two nodes disagree while both believe they
are in sync), and `Stop` closes the transport, which nothing did — a node left a
port bound and its goroutines running for the life of the process.

## Testing notes

A single-node cluster elects itself in about a second, so `leaderNode` polls for
leadership rather than sleeping. `freeAddr` reserves and releases a loopback port.
Every node gets `t.TempDir()` and a `t.Cleanup(Stop)`; without the latter the
package leaks a listener per test.

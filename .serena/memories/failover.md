# Failover, failback and the agent's orchestration role

What the agent is *for*, and where the implementation stops short of that intent.
Read with `mem:security` (boundary 3) and `mem:data_plane` (role detection).

## Intended role of the agent

The agent (`agent/`, `cmd/agent`, `:9091`) is the **database orchestrator**, not a metrics
sidecar. Its job is to install database software at a version the operator chooses, update
and tune it, initialise the primary, set up replication, and promote a replica during
failover — then have the write role reflected in the proxy. It runs as root on the database
host, which is why `agent_token` is mandatory on both ends.

The proxy is a separate concern: it moves queries and uses gpool. It does not install
anything.

## What actually happens on failover

`orchestration.FailoverManager.monitor` runs every 5s from `Start`:

1. **No healthy primary → `TriggerFailover`.** Picks the replica with the lowest lag,
   calls the agent's `PromoteToPrimary`, updates consensus, records `lastPromoted`, then
   nudges `ReevaluateRole()` on the promoted node and on any stale primary.
2. **More than one primary → split-brain resolution.** The loser is demoted to replica.

Step 1's `ReevaluateRole()` call is load-bearing and was missing. Promotion happens on the
database host, so the pool only learns a role changed on its own 30s deep-check tick — the
proxy kept routing writes to the backend that had just died for up to half a minute *after*
logging "Replica promoted successfully". `ReevaluateRole` pushes to `roleCheckChan`
(`pool/server.go`), which the maintenance loop turns into an immediate `deepCheck`.

## Failback does not exist, and the code used to imply it did

The split-brain branch was commented "Automatic Failback / Self-Healing". It is not
failback. A recovered old primary is **demoted to a replica** and the write role stays where
the failover put it. Nothing ever moves the write role back to a preferred node.

That is the safer default — auto-failback means a second unplanned outage, and Patroni and
friends do not do it either — but it is a real gap against the stated product intent, and it
should be a deliberate operator action rather than an emergent one.

Two things had to be fixed before that branch worked at all:

- **`m.consensus.GetPrimary()` was called with no nil guard**, while the leader check three
  lines above it has one. `registry.go:156` is the only production constructor and it passes
  `nil` — there is no Raft in a single-node deployment. Observed effect is not a clean panic:
  `monitor()` never returns, which wedges the goroutine that runs failover detection. `[repro]`
- **Config order picked the wrong winner.** With no consensus, the winner fell back to
  `healthyPrimaries[0]`, and backends are in config order — so a rebooted old primary beat
  the replica promoted to replace it. The failover was silently undone and writes went back
  to the node that just failed, after a replica had already taken writes on a diverged
  timeline. The manager now prefers `lastPromoted`. `[repro]`

## Still open

- No two-backend E2E topology, so promotion, replica routing and failover are only covered
  by unit tests with mock backends. This is the highest-value gap in the suite. The harness
  config (`e2e/harness_test.go`) declares one `role: primary` backend, and its `agent_addr`
  points at a port with nothing listening.
- The agent's own boundary *is* covered end to end now (`e2e/agent_test.go`): fail-closed
  startup, token rejection, allowlist enforcement. No database needed, ~9s.
- Management state (projects, users, settings) is not replicated by Raft between control
  plane nodes.
- `StateVerifying` resets to `StateIdle` after a hardcoded 30s sleep and verifies nothing.

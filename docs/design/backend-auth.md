# Design: Pontus-side backend authentication

Status: proposed · Findings addressed: **A8, A9, W2, W4**

## The problem, stated precisely

Pontus never performs a PostgreSQL startup exchange of its own. `Handshake` reads the
client's startup packet and relays the authentication messages between client and server
(`postgres_startup.go` frames SASL, md5 and cleartext correctly but never inspects them).
That exchange happens **once**, on the single connection acquired for the handshake.

Every other connection the pool creates is a raw socket that has negotiated nothing.

Three consequences, all measured rather than inferred:

- **A8** — a session that changes backend gets a socket the server will never answer on.
  Query 0 succeeds on the handshake connection, is released at the transaction boundary,
  query 1 routes to a replica, and the session dies with `conn closed`. Contained on
  2026-08-09 by refusing unusable connections and falling back to the handshake backend;
  the cost is that read/write splitting does not work at all.
- **W2/W4** — connections that *are* reusable were authenticated as whoever happened to open
  them. Reuse across clients works today only because deployments use one set of credentials.
  It is a cross-user data path.
- ~~**A9**~~ — **not part of this family.** It was diagnosed as connection reuse carrying
  state forward; it was the result cache answering extended-protocol messages, and it is
  fixed independently (2026-08-09). Connections are *not* reused across clients today.

One capability fixes all three: **Pontus must be able to open a backend connection as a given
user, without that user's client being present.**

## What each authentication method requires

This is the crux, and the SCRAM row is the one that decides the design.

| Server method | What Pontus needs to authenticate *to the server* | Available from a stored verifier? |
| :--- | :--- | :--- |
| `trust` | nothing | n/a |
| `password` (cleartext) | the plaintext password | **no** |
| `md5` | `md5(password‖username)` — exactly what `pg_authid.rolpassword` stores | **yes** |
| `scram-sha-256` | `ClientKey`, or the plaintext password | **not directly — see below** |
| `cert`, `ldap`, `gss`, `pam` | an external exchange Pontus cannot replay | **no** |

### SCRAM: recovering ClientKey from the client's own proof

A stored SCRAM verifier is `SCRAM-SHA-256$<iter>:<salt>$<StoredKey>:<ServerKey>`, and
`StoredKey = SHA256(ClientKey)`. A hash is one-way, so the verifier alone cannot authenticate
to a server. This is the point most designs get wrong.

But Pontus is not limited to the verifier — it also sees the client authenticate. In SCRAM:

```
ClientSignature = HMAC(StoredKey, AuthMessage)
ClientProof     = ClientKey XOR ClientSignature
```

Pontus knows `StoredKey` (from the verifier) and `AuthMessage` (it is on the wire), and the
client hands it `ClientProof`. So:

```
ClientKey = ClientProof XOR HMAC(StoredKey, AuthMessage)
```

Verifying `SHA256(ClientKey) == StoredKey` authenticates the client, and **keeping
`ClientKey` lets Pontus perform SCRAM as that user against a backend, for as long as it holds
it.** This is how pgbouncer's SCRAM pass-through works, and it means no plaintext password
ever has to be stored.

Two consequences that must be designed for, not discovered later:

- `ClientKey` is password-equivalent for that user. It must never be logged, never persisted,
  and must be zeroed when the session ends.
- **Channel binding breaks it.** `SCRAM-SHA-256-PLUS` binds the exchange to the client↔Pontus
  TLS channel, which Pontus terminates and cannot reproduce toward the server. Pontus must
  not advertise `-PLUS` when it intends to pass through, and must refuse rather than silently
  downgrade.

## Pool keying must change

A connection authenticated as `alice` cannot be handed to `bob`. Pools become keyed by
**(backend, database, user)** rather than by backend address alone (`pool.NewServer` takes
only an address today).

This is what actually closes W2.

### Decided: `max_conns` is per-pool

**`max_conns` becomes a per-(backend, database, user) ceiling** — pgbouncer's
`default_pool_size`. That is the decision; the rest of this section is what it forces.

The number of pools is driven by the **username in the startup packet**, which is
client-supplied. A per-pool ceiling with nothing above it therefore has no upper bound on
total connections: it is a map keyed by attacker-controlled input, the same shape as the
tenant rate-limiter that had to be bounded, except each entry costs a real connection on the
database. So per-pool requires two things above it, and they are part of the decision rather
than optional extras:

- **`max_backend_conns`** — a ceiling on connections to one backend across every pool
  (pgbouncer's `max_db_connections`). This is the number that keeps Pontus inside the
  database's own `max_connections`, and it must be enforced, not advisory.
- **A bound on the pool map itself**, with idle pools reaped on a TTL. A user who connects
  once must not hold a pool for the life of the process.

When `max_backend_conns` is reached, evict the least-recently-used **idle** pool rather than
refusing the new session. Refusing means a new user cannot connect while idle connections sit
unused for users who have gone away; eviction degrades in the right direction. Only when
nothing is idle does a new session wait, and then fail with a message naming the ceiling it
hit — never a silent hang.

**`min_idle` does not survive this unchanged.** Per-pool warm connections multiply by the
number of pools: `min_idle: 5` across forty users is two hundred idle connections against a
database that probably allows a hundred. `min_idle` must apply only to pools an operator has
declared, and default to **0** for pools created on demand by a user connecting. A warm-start
knob that quietly becomes a capacity bomb is worse than no warm start.

## Credential sources

- **`auth_query`** — a query run on a privileged connection, returning `(rolname, verifier)`.
  Pontus already has the right channel for this: `admin_dsn` per backend.
  Default: `SELECT rolname, rolpassword FROM pg_authid WHERE rolname = $1`.
  `pg_authid` requires superuser, so the documented setup is a `SECURITY DEFINER` function
  owned by a superuser and executable by a non-superuser `auth_user` — never "make admin_dsn
  a superuser".
- **`auth_file`** — a static user→verifier list, for deployments that will not grant even that.

Both feed one `CredentialStore` interface so the rest of the system does not care which.

### Cache requirements

The cache is keyed by a **client-supplied username**, which is the same shape as the
rate-limiter map that had to be bounded. It needs, non-negotiably:

- a maximum size with eviction,
- a positive TTL (so a changed password takes effect),
- **negative caching**, or an attacker walks unknown usernames and turns each one into a
  query against the primary — an auth-query amplification DoS.

## Staging

Each stage lands on its own, with its own verification, and leaves the system working.

**Stage 0 — pool keying by (backend, database, user).**
No new auth yet; connections are still only usable by the session that opened them. Fixes the
cross-user reuse in W2. Verify: a connection opened by one user is never handed to another.

*Premise, measured 2026-08-09:* connections are **not** reused across clients today — three
sessions, three PIDs, three `backend_start`s, and a connect/authorize/disconnect per session
in the server log. So this stage is not fixing a live cross-user leak; it is the keying that
makes reuse *safe to introduce* in Stage 4. Without it, the moment Pontus starts pooling
properly it starts handing one user's connection to another.

### Remaining work, scoped 2026-08-09

The keying itself is not yet done. It was attempted and backed out rather than landed
half-wired; what follows is what the attempt established, so the next run starts from here.

**Shape.** A `poolSet` on each `Server`, holding one `pooling.Core[*Conn]` per
`identity{user, database}`, with `maxTotal` (`max_backend_conns`), `maxPools`, an idle TTL
reaped on use, and LRU eviction of pools with no checked-out connections. `min_idle` is 0 for
on-demand pools.

**Blast radius.** `Server` touches its core in eight places — `Acquire`, `SetMaxConns`,
`EvictIdle`, `Stat` (twice), the deep-check `Acquire`, `Close`, and `controller.go`'s
`MaxConns()`. Seven external `.Acquire(` call sites, plus `Backend` gaining an identity-aware
acquire and the mocks that implement it.

**The trap that stopped it.** `Stats()` reports *cumulative* counters — `EmptyAcquireCount`,
`AcquireDuration`. Summing those across pools is fine until a pool is evicted, at which point
the totals go backwards. Eviction is not an edge case here, it is the capacity mechanism. So
`Server` needs accumulators that survive eviction, folding a pool's counters in as it is
closed. Decide that before writing the aggregation, not after.

Also note `pooling.Stat` has unexported fields and cannot be assembled outside its package,
so the aggregate needs its own type rather than a synthesised `pooling.Stat`.

**Sequencing.** Nothing exercises per-identity pools until Stage 3 gives Pontus the ability to
open a connection as a user; today every client already gets a fresh connection, so there is
no sharing to segregate. The safety property — never hand a session another identity's
connection — is already enforced at acquisition (below). Doing the keying immediately before
or with Stage 3 means it ships exercised rather than only unit-tested.

*Landed 2026-08-09 (first slice).* The acquire now happens **after** the client's startup
packet is read, so the user and database are known before a connection is chosen, and the
connection records the identity it authenticated as (`Conn.SetIdentity` / `BelongsTo`).
Mid-session acquisition refuses a connection belonging to another identity. Pools are still
keyed by address alone — the remaining work of this stage — but nothing can now hand one
user's connection to another session.

Protocols where the server speaks first cannot work this way, and that is expressed as a
capability: `protocol.StartupReader`. MySQL sends the greeting, so Pontus would have to
invent one before holding a backend to borrow it from; the MySQL handler does not implement
the interface and keeps the original order.

*Unblocked.* The apparent puzzle — a reused connection receiving a StartupMessage while
already in the query phase — did not exist: connections are not reused across clients, so
`Handshake` always forwards onto a fresh socket. The real work of this stage stands: the
client's identity is only known *after* the startup packet is read, yet the connection is
acquired before it, so the acquire has to move behind the read.

**Stage 1 — `CredentialStore`. Landed 2026-08-09** (`server/internal/credentials`).

`ParseVerifier` reads `pg_authid.rolpassword`: SCRAM-SHA-256, md5, or no password. A
plaintext value is refused rather than guessed at — guessing means authenticating with
something that is not a credential — and a password beginning "md5" is not mistaken for a
hash. `Verifier.String()` redacts, because the SCRAM keys verify a client's proof and yield
its ClientKey, which is password-equivalent and reaches logs by accident.

`QueryStore` binds the user name as a parameter, never interpolates it: the name arrives in a
startup packet, so string-building the query would be SQL injection reachable before
authentication. `FileStore` refuses a group- or world-readable file and rejects a whole file
on one bad line, since a half-applied credential file locks out whichever roles followed it.

`Cache` is bounded, expiring, and caches misses — all three are security properties, not
optimisations. The key is a client-chosen user name, so an unbounded map is a remote memory
exhaustion; and without negative caching an attacker walking a username list turns each cheap
TCP connection into a query against the primary, with Pontus as the amplifier. Transport
failures are deliberately *not* cached: one blip would otherwise lock a deployment out for
the whole TTL.

No behaviour change to the data plane, and no config surface yet — that arrives in Stage 2
with the code that reads it, so the wiring test cannot start passing on a setting nothing
consumes.

*Verified against a live PostgreSQL, not just unit tests:* the default query parses a real
SCRAM verifier, an unknown role is reported as such, and the SECURITY DEFINER recipe below
works from a non-superuser role that provably **cannot** read `pg_authid` directly.

```sql
CREATE OR REPLACE FUNCTION pontus_auth_lookup(IN wanted text,
    OUT rolname text, OUT verifier text)
RETURNS record AS $$
  SELECT rolname::text, coalesce(rolpassword, '')::text
    FROM pg_authid WHERE rolname = wanted
$$ LANGUAGE sql SECURITY DEFINER STABLE;

REVOKE EXECUTE ON FUNCTION pontus_auth_lookup(text) FROM PUBLIC;
CREATE ROLE pontus_auth LOGIN PASSWORD '...';
GRANT EXECUTE ON FUNCTION pontus_auth_lookup(text) TO pontus_auth;
```

with `auth_query: SELECT rolname, verifier FROM pontus_auth_lookup($1)`.

**Stage 2 — Pontus authenticates the client.** *SCRAM exchange landed 2026-08-09;
wire integration still to do.*

`credentials.ScramServer` is the server half of SCRAM-SHA-256, and it recovers `ClientKey`
from the client's proof exactly as described above. That premise is no longer an assertion:
a test derives a verifier from a password, has an independently-implemented client compute a
proof, and checks the server recovers the same `ClientKey` the client used — and a second
test does it against a verifier **PostgreSQL generated** (4096 iterations, 16-byte salt), so
the interoperability is real rather than self-consistent.

Refusals implemented and tested: channel binding announced up front (`p=`), a binding
asserted only in the final message (a downgrade attempt between the two), a nonce from
another exchange (a replay), a proof of the wrong length, and every malformed shape. One
error for every failure — telling a caller whether the user existed, or how far the proof
got, is an oracle.

Remaining for this stage: the PostgreSQL message framing (AuthenticationSASL /
SASLInitialResponse / SASLContinue / SASLResponse / SASLFinal / AuthenticationOk), `md5` and
`trust` toward the client, the config surface (`auth_query`, `auth_file`, cache settings),
and the rule that Pontus never offers a weaker method than the backend would. Verify against
psql, pgx, JDBC and asyncpg.

**Stage 3 — Pontus opens backend connections.** *Core landed 2026-08-09.*

`protocol.AuthenticateBackend` performs a full startup exchange as a given user from a
`ClientKey` alone — no password — and `credentials.ScramClient` is the client half that
produces the proof. The backend is authenticated in return via ServerKey, because skipping
that would let anything answering on the backend's address complete the exchange.

**Proven against a real server, not by arithmetic that agrees with itself:** a live test
creates a role, reads its verifier through `auth_query`, recovers a `ClientKey` by verifying
a client proof, and then opens a TCP connection to PostgreSQL 17.10 and authenticates with
the key alone, through to `ReadyForQuery` and `server_version`. A fabricated key is refused
*by the server*.

Remaining: replacing the relay in `handleClient` — which is where Stages 2 and 3 meet, see
below — plus md5 (a stored md5 verifier can answer an md5 challenge directly) and the
per-identity pooling that lets the resulting connections actually be shared.

### Stages 2 and 3 are coupled at the wire

Worth stating because the staging implies otherwise. The moment Pontus authenticates the
client itself, the client's startup packet can no longer be forwarded verbatim — its
authentication exchange has been consumed, and a backend will not accept a startup whose
auth reply never arrives. So the client-facing switch cannot land before the backend-facing
side exists.

**The swap landed 2026-08-09**, behind `auth.mode: pontus` (default `passthrough`).
`openSession` authenticates the client with `ScramServer` *before* acquiring a backend —
an unauthenticated client must not be able to make Pontus open a database connection, or
anyone who can reach the port can exhaust the pool without proving who they are — then opens
the backend with `AuthenticateBackend` and completes the client's startup from the
parameters the backend reported.

Verified with a real pgx driver: it authenticates against Pontus, Pontus opens its own
backend connection with the recovered ClientKey, and the session serves ten statements. A
wrong password and an unknown role are refused *identically*, so the error does not
enumerate real accounts. Passthrough with no auth block behaves exactly as before.

Still open for this stage:

- **md5 toward the client.** A stored md5 verifier can answer an md5 challenge directly, but
  it is a different exchange in both directions. Refused with its reason rather than
  downgraded, which excludes pre-14 deployments.
- **The driver matrix is started, not finished.** pgx and **libpq** (psql 17.10) both pass,
  the latter reliably — three consecutive runs after the harness bug below was fixed. That
  matters more than pgx: nearly every PostgreSQL tool is built on libpq, and it has never
  seen this code, so it tests correctness rather than self-consistency.

  **asyncpg fails, and that is finding A10.** It is the most valuable third opinion — a pure
  Python SCRAM implementation sharing no code with either — and it cannot complete a session:
  the connect hangs, and a refused login reports `protocol.data_received() call failed`,
  which is a protocol error rather than an authentication one. Pontus emits something after
  AuthenticationOk that asyncpg models more strictly than pgx and libpq do. Supplying
  BackendKeyData, which was genuinely missing, did not fix it.

  JDBC remains untested. Until A10 is closed, `auth.mode: pontus` should not be recommended
  and asyncpg clients must not be pointed at it.

  The interpreter is discovered, never installed: resolving asyncpg on demand with `uv` cost
  longer than the suite's budget and left Go blocked on a pipe inherited by the killed child.
  Create one with `uv venv /tmp/pontus-drivers --python 3.12 && uv pip install --python
  /tmp/pontus-drivers/bin/python asyncpg`, or point `PONTUS_E2E_PYTHON` at one.

  *The bug that hid this:* `e2e.containerRuntime` picked the first runtime binary on PATH
  without checking it worked, and Podman is commonly installed and not running. Every call
  then failed with "cannot connect to Podman", which reads as a Pontus failure.
  `scripts/e2e-cluster.sh` had always probed with `info`/`system status`; the Go helper had
  not. Worth remembering as a shape: *installed* is not *working*, and a test that cannot
  tell an environment fault from a real one trains you to ignore it.

**Stage 4 — safe reuse between clients.**
Reuse only becomes possible at this stage — today every client gets a fresh backend
connection, which is why nothing leaks between them yet. Once connections are shared:
`DISCARD ALL` on release, clear the tracked statement set, and fix `ReplaySessionState` so a
client's own `SET` survives. These must land together — the earlier attempt was reverted
because resetting without working replay broke `SET` within a single session. Verify: a
connection handed to a second client carries none of the first's prepared statements,
session variables or temp tables, and a client's own `SET` still survives its session.

**Stage 5 — remove the containment.**
`acquireForSession`'s fallback and its warning become unnecessary. Verify: the read/write
split under a real two-node cluster, and `pontus_routed_requests_total` showing reads served
by `role="replica"`.

## What Pontus will refuse

Explicitly, at startup or at connect time, with a message naming the reason:

- `cert`, `ldap`, `gss`, `pam` — cannot be replayed toward a backend.
- `scram-sha-256-plus` with channel binding, when pass-through is required.
- `password` (cleartext to the backend) unless a plaintext credential source is configured.

Refusing loudly matters more than covering every method: a pooler that silently downgrades
authentication is worse than one that declines to start.

## Open questions

1. ~~Capacity.~~ **Decided:** `max_conns` is per-pool, with `max_backend_conns` above it,
   LRU eviction of idle pools, and `min_idle` defaulting to 0 for on-demand pools. See
   "Decided: `max_conns` is per-pool" above.
2. **`ClientKey` lifetime.** Held for the client session only, or cached per user to allow
   opening connections after that client disconnects? The second is more useful for warming
   pools and strictly worse for blast radius. Default should be session-scoped.
3. **Rotation.** A password changed in the database invalidates cached verifiers only after
   the TTL. Is that acceptable, or does `pontusctl` need a cache-flush command?
4. **MySQL.** This design is PostgreSQL-specific. The MySQL handler needs its own answer, and
   until it has one its pooling carries the same defect.

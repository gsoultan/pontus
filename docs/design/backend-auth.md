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
- **A9** — reuse between clients also carries prepared statements and session state forward,
  because nothing owns a connection's lifecycle enough to reset it.

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

This is what actually closes W2, and it has a capacity consequence worth stating up front:
idle connections no longer amortise across users. `max_conns` becomes a per-(user, database)
ceiling with a global cap above it, or a busy multi-tenant deployment will exhaust the
database's `max_connections`.

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

**Stage 1 — `CredentialStore`.**
`auth_query` and `auth_file`, with the bounded cache above. No behaviour change yet.
Verify: lookups against a real `pg_authid`, cache eviction, negative caching, and that a
non-superuser `auth_user` with the SECURITY DEFINER function works.

**Stage 2 — Pontus authenticates the client.**
Replace relaying with a real server-side implementation of `trust`, `md5` and
`scram-sha-256`, recovering and holding `ClientKey`. Pontus must never offer the client a
weaker method than the backend would have. Verify: psql, pgx, JDBC and asyncpg all
authenticate; a wrong password is refused; `-PLUS` is refused rather than downgraded.

**Stage 3 — Pontus opens backend connections.**
Startup packet plus authentication as the user, using the md5 verifier or the recovered
`ClientKey`. This is the stage that removes A8's cause. Verify:
`e2e.TestReadsReachTheReplica` is un-skipped and passes — a read reaches a real standby.

**Stage 4 — safe reuse between clients (A9).**
Now that Pontus owns the lifecycle: `DISCARD ALL` on release, clear the tracked statement
set, and fix `ReplaySessionState` so a client's own `SET` survives. These must land together
— the earlier attempt reverted precisely because resetting without working replay broke
`SET` within a single session. Verify: the second client to run the same SQL succeeds
(currently `26000`), and a client's own `SET` survives its session.

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

1. **Capacity.** Per-(user, database) pools multiply idle connections. Does `max_conns`
   become per-pool with a global ceiling, or does the global ceiling stay and pools borrow
   from it?
2. **`ClientKey` lifetime.** Held for the client session only, or cached per user to allow
   opening connections after that client disconnects? The second is more useful for warming
   pools and strictly worse for blast radius. Default should be session-scoped.
3. **Rotation.** A password changed in the database invalidates cached verifiers only after
   the TTL. Is that acceptable, or does `pontusctl` need a cache-flush command?
4. **MySQL.** This design is PostgreSQL-specific. The MySQL handler needs its own answer, and
   until it has one its pooling carries the same defect.
